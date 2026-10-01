// SPDX-License-Identifier: Apache-2.0

package pubsub

import (
	"context"
	"time"
)

// Broker is a message broker: the memory broker is built in, and driver
// modules provide others (redis.PubSubDriver(), gcppubsub.Driver()).
//
// A topic is a stream of messages; a subscription is a named reader of a
// topic that gets each message once, shared by every process listening
// with that name. Two subscriptions of one topic each get every message.
// Delivery is at-least-once. The pubsub/pubsubtest package is a
// conformance suite for brokers.
type Broker interface {
	// Publish adds m to topic and returns the message's ID.
	Publish(ctx context.Context, topic string, m Outgoing) (string, error)
	// Prepare creates the subscription s if the broker creates
	// subscriptions (and it doesn't exist), so messages published from
	// now on are kept for it. A subscription gets the messages published
	// after it was created.
	Prepare(ctx context.Context, s SubscriptionSpec) error
	// Subscribe delivers the messages of s to handle until ctx ends: at
	// most s.Concurrency at once (it stops taking messages while that many
	// are being handled), each settled by the Outcome handle returns. A
	// message being handled for longer than s.AckTimeout may be delivered
	// again. Subscribe returns once ctx has ended and every call to handle
	// has returned, or after an error it can't recover from (the listener
	// then starts it again).
	Subscribe(ctx context.Context, s SubscriptionSpec, handle func(ctx context.Context, m *Message) Outcome) error
	// Close releases the broker's resources.
	Close() error
}

// Outgoing is a message to publish.
type Outgoing struct {
	// Data is the message's body.
	Data []byte
	// Attributes are string pairs sent with it.
	Attributes map[string]string
}

// Message is a message received from a subscription.
type Message struct {
	// ID identifies the message in its topic.
	ID string
	// Topic is the topic it was published to.
	Topic string
	// Data is its body.
	Data []byte
	// Attributes are the string pairs sent with it.
	Attributes map[string]string
	// Attempt counts its deliveries to the subscription, this one
	// included; 0 when the broker doesn't count them.
	Attempt int
	// PublishedAt is when it was published, by the broker's clock.
	PublishedAt time.Time
}

// SubscriptionSpec describes the subscription a listener reads.
type SubscriptionSpec struct {
	// Topic is the topic to read.
	Topic string
	// Name is the subscription's name.
	Name string
	// Concurrency is how many messages may be handled at once.
	Concurrency int
	// AckTimeout is how long a message may be handled before the broker
	// may deliver it again.
	AckTimeout time.Duration
}

// Outcome settles a delivered message.
type Outcome struct {
	// Ack removes the message from the subscription: it was handled (or
	// given up on).
	Ack bool
	// RetryAfter, when Ack is false, is when the message should be
	// delivered again. Brokers that can't delay a delivery deliver it
	// again as their own policy says.
	RetryAfter time.Duration
}
