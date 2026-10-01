// SPDX-License-Identifier: Apache-2.0

// Package mailer sends email. An email is a Mailable: a type that builds
// a Message (recipients, subject, an HTML body from a templ component, a
// text body, attachments) from its fields. Send sends it now; Queue
// renders it now and sends it from a queue job, with retries.
//
//	m, err := mailer.ForApp(app) // MAIL_DRIVER: log (default), smtp, memory, or a driver module's
//
//	err = mailer.Send(ctx, mails.Welcome{User: u})
//	err = mailer.Queue(ctx, mails.Receipt{Order: o}, queue.OnQueue("emails"))
//
// Transports send rendered emails: the log (development), SMTP, memory
// (tests) and, in driver modules, email APIs (drivers/postmark). The
// mailer travels in the context, like the database and the cache.
//
// The package is named mailer, not mail, so it doesn't shadow net/mail.
package mailer
