// SPDX-License-Identifier: Apache-2.0

// Package pubsub publishes messages to topics of a message broker and
// runs listeners of its subscriptions: for streams other services share,
// such as "orders.created", where the queue package is for an app's own
// jobs.
//
//	ps, err := pubsub.New(app, redis.PubSubDriver(), gcppubsub.Driver()) // PUBSUB_DRIVER picks one
//	err = pubsub.Listen(ps, "orders.created", billing.OrderCreated,
//		pubsub.Concurrency(20), pubsub.MaxAttempts(5), pubsub.DeadLetter("orders.created.dlq"))
//
//	err = pubsub.Publish(ctx, "invoices.created", InvoiceCreated{ID: inv.ID})
//
// Listeners get messages decoded from JSON, each subscription's at most
// Concurrency at once; returning nil acknowledges a message, an error has
// it delivered again after a backoff. Delivery is at-least-once, so
// listeners must be idempotent. The brokers: memory (in the process, for
// development and tests), Redis Streams (drivers/redis) and Google Cloud
// Pub/Sub (drivers/gcppubsub).
package pubsub
