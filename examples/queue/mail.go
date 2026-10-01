// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"anetos.dev/anetos/db"
	"anetos.dev/anetos/mailer"
	"anetos.dev/anetos/web"
)

// region: mailable
// ReceiptMail is the email of an order's receipt. Its fields are what it
// needs; Build turns them into the message, with the HTML body from the
// ReceiptEmail templ component (receipt.templ). The text body is made
// from the HTML.
type ReceiptMail struct {
	Order OrderPlaced
}

// Build implements mailer.Mailable.
func (m ReceiptMail) Build(ctx context.Context) (*mailer.Message, error) {
	return &mailer.Message{
		To:       []mailer.Address{{Address: m.Order.Email}},
		Subject:  fmt.Sprintf("Your receipt for order %d", m.Order.OrderID),
		HTML:     ReceiptEmail(m.Order),
		Tag:      "receipt",
		Metadata: map[string]string{"order_id": strconv.FormatInt(m.Order.OrderID, 10)},
	}, nil
}

// endregion

// price formats cents as dollars.
func price(cents int64) string { return fmt.Sprintf("$%d.%02d", cents/100, cents%100) }

// region: queue
// ResendReceipt emails an order's receipt again. mailer.Queue renders the
// email now and sends it from a queue job: the request doesn't wait for
// the mail server, and a failure is retried.
func ResendReceipt(c *web.Ctx, in OrderID) (web.Responder, error) {
	o, err := db.Find[Order](c, in.ID)
	if err != nil {
		return nil, err
	}
	e := OrderPlaced{OrderID: o.ID, Item: o.Item, Cents: o.Cents, Email: o.Email}
	if err := mailer.Queue(c, ReceiptMail{Order: e}); err != nil {
		return nil, err
	}
	return web.JSON(http.StatusAccepted, map[string]string{"receipt": "queued"}), nil
}

// endregion

// previewReceipt is a receipt with made-up values, for GET
// /dev/mail/receipt in development.
func previewReceipt(*http.Request) mailer.Mailable {
	return ReceiptMail{Order: OrderPlaced{OrderID: 42, Item: "Lamp", Cents: 4250, Email: "ada@example.com"}}
}
