// SPDX-License-Identifier: Apache-2.0

// Package events delivers typed, in-process events to listeners, so the
// code that does something (place an order) doesn't need to know
// everything that follows from it (an email, a stock update, analytics).
//
// An event is any type, usually a struct; listeners are functions of it:
//
//	bus, err := events.New(app)
//	err = events.On(bus, recordAudit)                                  // in Emit, in its transaction
//	err = events.OnAsync(bus, countSale, events.Concurrency(4))        // in the background, after the commit
//	err = events.OnQueued(bus, notifyWarehouse)                        // as a queue job: durable, retried
//
//	err = events.Emit(ctx, OrderPlaced{OrderID: o.ID})
//
// Listeners are found by the event's type, in a map built as they are
// added. On listeners run in Emit and can fail it. OnAsync listeners run
// in a goroutine pool of their own, after the transaction commits; their
// events are lost if the process stops first. OnQueued listeners run as
// jobs of the queue package, with its retries and failed jobs.
package events
