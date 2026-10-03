// SPDX-License-Identifier: Apache-2.0

// Package gcppubsub is the Google Cloud Pub/Sub broker of the pubsub
// package:
//
//	ps, err := pubsub.ForApp(app, gcppubsub.Driver())
//
// Settings: PUBSUB_DRIVER=gcp, PUBSUB_GCP_PROJECT (the project's ID), and
// PUBSUB_GCP_CREATE=true to create missing topics and subscriptions (for
// development and the emulator; in production, create them with your
// infrastructure tools). Credentials come from Application Default
// Credentials; PUBSUB_EMULATOR_HOST selects the emulator.
package gcppubsub

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/pubsub"
	gpubsub "cloud.google.com/go/pubsub/v2"
	"cloud.google.com/go/pubsub/v2/apiv1/pubsubpb"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
)

// Config is the broker's settings.
type Config struct {
	// Project is the Google Cloud project's ID. PUBSUB_GCP_PROJECT.
	Project string `env:"PUBSUB_GCP_PROJECT"`
	// Create creates missing topics and subscriptions. PUBSUB_GCP_CREATE,
	// default false.
	Create bool `env:"PUBSUB_GCP_CREATE" default:"false"`
}

// Driver is the broker's driver (PUBSUB_DRIVER=gcp). opts configure the
// client (tests pass option.WithGRPCConn for a fake server).
func Driver(opts ...option.ClientOption) pubsub.Driver {
	return pubsub.Driver{Name: "gcp", Open: func(app *anetos.App, cfg pubsub.Config) (pubsub.Broker, error) {
		c, err := config.Get[Config](app.Source())
		if err != nil {
			return nil, err
		}
		if c.Project == "" {
			return nil, errors.New("PUBSUB_GCP_PROJECT is required with PUBSUB_DRIVER=gcp")
		}
		client, err := gpubsub.NewClient(context.Background(), c.Project, opts...)
		if err != nil {
			return nil, err
		}
		return NewBroker(client, cfg.Prefix, c.Create), nil
	}}
}

// Broker is a pub/sub broker on Google Cloud Pub/Sub. Topic and
// subscription IDs are the names with the prefix, with "%" and the
// characters Pub/Sub doesn't allow in IDs (such as ":") escaped as "%XX".
//
// The client extends ack deadlines while a message is handled, up to the
// listener's ack timeout. A failed message is nacked: Pub/Sub delivers it
// again as the subscription's retry policy says (the listener's Backoff
// doesn't apply). Message.Attempt is set only for subscriptions with a
// dead-letter policy; without one, the listener's MaxAttempts can't apply.
type Broker struct {
	client *gpubsub.Client
	prefix string
	create bool

	mu         sync.Mutex
	publishers map[string]*gpubsub.Publisher
}

// NewBroker returns a broker using client, with IDs starting with prefix;
// with create, it creates missing topics and subscriptions. Closing the
// broker closes the client.
func NewBroker(client *gpubsub.Client, prefix string, create bool) *Broker {
	return &Broker{client: client, prefix: prefix, create: create, publishers: map[string]*gpubsub.Publisher{}}
}

// id returns the Pub/Sub ID of a topic or subscription name: the prefix
// and the name, with "%" and the characters IDs can't have escaped as
// "%XX", so different names never share an ID.
func (b *Broker) id(name string) (string, error) {
	var sb strings.Builder
	for _, c := range []byte(b.prefix + name) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', strings.IndexByte("-_.~+", c) >= 0:
			sb.WriteByte(c)
		default:
			fmt.Fprintf(&sb, "%%%02X", c)
		}
	}
	id := sb.String()
	letter := (id[0] >= 'a' && id[0] <= 'z') || (id[0] >= 'A' && id[0] <= 'Z') // checkName in pubsub makes names non-empty
	if len(id) < 3 || len(id) > 255 || !letter || strings.HasPrefix(strings.ToLower(id), "goog") {
		return "", fmt.Errorf("gcppubsub: %q isn't a valid Pub/Sub ID: it must have 3 to 255 characters, start with a letter, and not start with \"goog\"", id)
	}
	return id, nil
}

func (b *Broker) topicPath(name string) (string, error) {
	id, err := b.id(name)
	return "projects/" + b.client.Project() + "/topics/" + id, err
}

func (b *Broker) subPath(name string) (string, error) {
	id, err := b.id(name)
	return "projects/" + b.client.Project() + "/subscriptions/" + id, err
}

func (b *Broker) publisher(path string) *gpubsub.Publisher {
	b.mu.Lock()
	defer b.mu.Unlock()
	p := b.publishers[path]
	if p == nil {
		p = b.client.Publisher(path)
		b.publishers[path] = p
	}
	return p
}

// Publish implements [pubsub.Broker]. With create, a missing topic is
// created.
func (b *Broker) Publish(ctx context.Context, topic string, m pubsub.Outgoing) (string, error) {
	path, err := b.topicPath(topic)
	if err != nil {
		return "", err
	}
	msg := &gpubsub.Message{Data: m.Data, Attributes: m.Attributes}
	id, err := b.publisher(path).Publish(ctx, msg).Get(ctx)
	if status.Code(err) == codes.NotFound && b.create {
		if err := b.ensureTopic(ctx, path); err != nil {
			return "", err
		}
		msg = &gpubsub.Message{Data: m.Data, Attributes: m.Attributes}
		id, err = b.publisher(path).Publish(ctx, msg).Get(ctx)
	}
	return id, err
}

// ensureTopic creates the topic path if it doesn't exist.
func (b *Broker) ensureTopic(ctx context.Context, path string) error {
	_, err := b.client.TopicAdminClient.CreateTopic(ctx, &pubsubpb.Topic{Name: path})
	if status.Code(err) == codes.AlreadyExists {
		return nil
	}
	return err
}

// Prepare implements [pubsub.Broker]. With create, it creates the
// subscription (and its topic) if it doesn't exist, with the ack
// deadline of the listener's ack timeout (10s to 600s) and a retry policy
// backing off from 10s to 600s; that needs the pubsub.subscriptions.get
// and .create and pubsub.topics.create permissions. Without create, it
// only checks the names: a missing subscription shows when the listener
// starts.
func (b *Broker) Prepare(ctx context.Context, s pubsub.SubscriptionSpec) error {
	subPath, err := b.subPath(s.Name)
	if err != nil {
		return err
	}
	topicPath, err := b.topicPath(s.Topic)
	if err != nil || !b.create {
		return err
	}
	_, err = b.client.SubscriptionAdminClient.GetSubscription(ctx, &pubsubpb.GetSubscriptionRequest{Subscription: subPath})
	if status.Code(err) != codes.NotFound {
		return err
	}
	if err := b.ensureTopic(ctx, topicPath); err != nil {
		return err
	}
	deadline := min(max(s.AckTimeout, 10*time.Second), 600*time.Second)
	_, err = b.client.SubscriptionAdminClient.CreateSubscription(ctx, &pubsubpb.Subscription{
		Name:               subPath,
		Topic:              topicPath,
		AckDeadlineSeconds: int32(deadline / time.Second),
		RetryPolicy:        &pubsubpb.RetryPolicy{MinimumBackoff: durationpb.New(10 * time.Second), MaximumBackoff: durationpb.New(600 * time.Second)},
	})
	if status.Code(err) == codes.AlreadyExists {
		return nil
	}
	return err
}

// Subscribe implements [pubsub.Broker].
func (b *Broker) Subscribe(ctx context.Context, s pubsub.SubscriptionSpec, handle func(ctx context.Context, m *pubsub.Message) pubsub.Outcome) error {
	subPath, err := b.subPath(s.Name)
	if err != nil {
		return err
	}
	sub := b.client.Subscriber(subPath)
	sub.ReceiveSettings.MaxOutstandingMessages = max(s.Concurrency, 1)
	sub.ReceiveSettings.NumGoroutines = 1
	sub.ReceiveSettings.MaxExtension = s.AckTimeout
	err = sub.Receive(ctx, func(ctx context.Context, gm *gpubsub.Message) {
		m := &pubsub.Message{ID: gm.ID, Topic: s.Topic, Data: gm.Data, Attributes: gm.Attributes, PublishedAt: gm.PublishTime}
		if gm.DeliveryAttempt != nil {
			m.Attempt = *gm.DeliveryAttempt
		}
		if handle(ctx, m).Ack {
			gm.Ack()
		} else {
			gm.Nack()
		}
	})
	if ctx.Err() != nil {
		return nil //nolint:nilerr // stopped: Receive's error is the cancellation
	}
	return err
}

// Close implements [pubsub.Broker]: it stops the publishers and closes
// the client.
func (b *Broker) Close() error {
	b.mu.Lock()
	for _, p := range b.publishers {
		p.Stop()
	}
	b.publishers = map[string]*gpubsub.Publisher{}
	b.mu.Unlock()
	return b.client.Close()
}
