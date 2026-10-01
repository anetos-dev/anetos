---
title: Send email
since: v0.2.0
---

# Send email

Send email from your app: a receipt, a welcome message, a password reset
link. An email is a **mailable**, a type that builds its message from its
fields, with an HTML body from a templ component. Send it now, or queue
it so a worker sends it with retries. The complete app is
[`examples/queue`](../../../examples/queue), which emails a receipt for
each order.

## Before you start

You have an app created with `anetos.New()`. For queued email, set up the
[queue](queues.md) first.

Choose how emails are sent with `MAIL_DRIVER`:

| Driver | Emails are | Use it for |
|---|---|---|
| `log` (default) | Written to the app's log, with their text body: not sent | Development |
| `smtp` | Sent to the SMTP server of `MAIL_SMTP_URL` | Production with any provider; development with Mailpit |
| `memory` | Kept in the process | Tests (`anetostest` sets it) |
| `postmark` | Sent with Postmark's API (`drivers/postmark`, `MAIL_POSTMARK_TOKEN`) | Production with Postmark |

Set the sender of your emails with `MAIL_FROM_ADDRESS` (and
`MAIL_FROM_NAME`, which defaults to `APP_NAME`), and `APP_URL`, the
app's public URL, for links in emails:

```env
MAIL_DRIVER=smtp
MAIL_SMTP_URL=smtps://apikey:secret@smtp.example.com:465
MAIL_FROM_ADDRESS=orders@shop.example.com
APP_URL=https://shop.example.com
```

To see emails in development without sending them anywhere, run
[Mailpit](https://mailpit.axllent.org) and set `MAIL_DRIVER=smtp`: the
default `MAIL_SMTP_URL` is Mailpit's, `smtp://127.0.0.1:1025`.

## Steps

### 1. Write the email's template

The HTML body is a templ component. Mail clients ignore most CSS, so use
inline styles and a simple layout:

```templ
// ReceiptEmail is the receipt's HTML body. Mail clients ignore most CSS,
// so the styles are inline and the layout is one column. Links leave the
// app: mailer.URL makes them absolute, with APP_URL.
templ ReceiptEmail(e OrderPlaced) {
	<!DOCTYPE html>
	<html>
		<body style="margin:0;padding:24px;background:#f4f4f5;font-family:Arial,sans-serif;color:#18181b">
			<div style="max-width:560px;margin:0 auto;padding:24px;background:#ffffff;border-radius:8px">
				<h1 style="margin:0 0 16px;font-size:20px">Thanks for your order</h1>
				<p>Order { strconv.FormatInt(e.OrderID, 10) }: { e.Item }, { price(e.Cents) }.</p>
				<p>We'll charge your card shortly.</p>
				<p><a href={ mailer.URL(ctx, "/orders/"+strconv.FormatInt(e.OrderID, 10)) } style="color:#2563eb">See your order</a></p>
			</div>
		</body>
	</html>
}
```

(Copied from [`examples/queue/receipt.templ`](../../../examples/queue/receipt.templ), region `template`.)

`mailer.URL(ctx, path)` returns the absolute URL of a path on `APP_URL`,
or an error without it, which fails the send. Links in emails leave the
app, so relative links don't work.

### 2. Write a mailable

A mailable is a type with a `Build` method that returns the message:

```go
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
```

(Copied from [`examples/queue/mail.go`](../../../examples/queue/mail.go), region `mailable`.)

`Build` gets the context of the send, with the app's values: it can read
the database. The message's fields:

| Field | |
|---|---|
| `To`, `Cc`, `Bcc` | Recipients, `mailer.Address{Name, Address}`: at least one in all |
| `From`, `ReplyTo` | The sender (default `MAIL_FROM_ADDRESS`), and where replies go |
| `Subject` | The subject line |
| `HTML` | The HTML body, a templ component (or any `view.Component`) |
| `Text` | The plain-text body, a string (build it with `fmt` or `text/template`; templ would HTML-escape it). Without it, the text of the HTML: paragraphs, lists, and links followed by their URL |
| `Attachments` | Files: `mailer.Attachment{Filename, Data}` (the content type comes from the name); with a `ContentID`, inline, shown by `<img src="cid:…">` |
| `Headers` | Extra headers, such as `List-Unsubscribe` (not those the message sets: `From`, `Subject`, `Content-Type`, …) |
| `Tag`, `Metadata` | Labels that API drivers keep with the email (Postmark) |

A `*mailer.Message` is a mailable too, for one-off emails.

### 3. Set up the mailer

In `setup`; with `queue.ForApp` too, before or after it, for queued email:

```go
// With the queue, mailer.Queue sends from a queue job.
if _, err := mailer.ForApp(app, postmark.Driver()); err != nil { // MAIL_DRIVER: log, smtp, memory or postmark
	return nil, err
}
```

(Copied from [`examples/queue`](../../../examples/queue/main.go), region `mail-setup`.)

Drivers in other modules, such as `postmark.Driver()`, are passed to
`mailer.ForApp`; `MAIL_DRIVER` picks one.

### 4. Send or queue

`mailer.Send(ctx, mailable)` sends now and returns the transport's error.
In a queue job, that error makes the job retry:

```go
// recordAudit writes to the audit log in the order's transaction: if it
// fails, the order isn't placed.
func recordAudit(ctx context.Context, e OrderPlaced) error {
	return db.Create(ctx, &AuditEntry{OrderID: e.OrderID, Message: "placed: " + e.Item})
}

// countSale updates the sales counts in the background, once the order
// is committed. If the process stops first, the count is lost: fine for
// a dashboard.
func (s *Sales) countSale(_ context.Context, e OrderPlaced) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Orders++
	s.Cents += e.Cents
	return nil
}

// emailReceipt sends the receipt, as a queue job: retried if the mail
// server fails, and not lost if the process stops.
func emailReceipt(ctx context.Context, e OrderPlaced) error {
	return mailer.Send(ctx, ReceiptMail{Order: e})
}
```

(Copied from [`examples/queue/events.go`](../../../examples/queue/events.go), region `listeners`.)

In a request, queue the email instead: `mailer.Queue` renders it now
and dispatches a job that sends it, so the request doesn't wait for the
mail server, and a failure is retried:

```go
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
```

(Copied from [`examples/queue/mail.go`](../../../examples/queue/mail.go), region `queue`.)

`mailer.Queue` takes the dispatch's options: `queue.OnQueue("emails")`,
`queue.Delay(d)`, `queue.AfterCommit()`. The email's `Date` is when the
job sends it. Errors that retrying won't fix, such as an address the
server rejects, fail the job at once.

The job carries the rendered email, attachments included, and a job
that fails for good keeps it in the failed jobs (`queue:failed`): think
of that for emails with password reset links. For big attachments,
dispatch a job of your own that builds the email and calls
`mailer.Send`.

### 5. Preview emails

`mailer.Preview` returns a handler that shows a mailable's HTML body in
the browser. Serve it only in development:

```go
if app.Config().Env.IsDevelopment() {
	r.HandleStd(http.MethodGet, "/dev/mail/receipt", mailer.Preview(previewReceipt))
}
```

(Copied from [`examples/queue`](../../../examples/queue/main.go), region `preview`.)

`previewReceipt` is a `func(*http.Request) mailer.Mailable` that returns
the email with made-up values.

### 6. Test

`anetostest` sets `MAIL_DRIVER=memory`: emails are kept, not sent. The
mailer's transport is then a `*mailer.MemoryTransport`, whose `Sent`
returns them, rendered:

```go
// The receipt is queued (the sync driver sends it at once) and kept by
// the memory transport: the test checks its recipient, subject and body.
func TestResendReceipt(t *testing.T) {
	fakeGateway(t, &FakeGateway{})
	app := anetostest.New(t, setup, anetostest.Env(map[string]string{"QUEUE_DRIVER": "sync"}))
	id := placeOrder(t, app, "Lamp", 4250)

	app.PostJSON(fmt.Sprintf("/orders/%d/receipt", id), nil).AssertStatus(http.StatusAccepted)

	sent := sentMail(app)
	if len(sent) != 2 { // when placed, and again
		t.Fatalf("%d emails", len(sent))
	}
	m := sent[1]
	if m.To[0].Address != "ada@example.com" || m.Subject != fmt.Sprintf("Your receipt for order %d", id) {
		t.Errorf("email %+v", m)
	}
	for _, want := range []string{"Lamp, $42.50", fmt.Sprintf("See your order (http://localhost/orders/%d)", id)} {
		if !strings.Contains(m.Text, want) {
			t.Errorf("the text lacks %q:\n%s", want, m.Text)
		}
	}
	app.PostJSON("/orders/999/receipt", nil).AssertNotFound()
}
```

(Copied from [`examples/queue/main_test.go`](../../../examples/queue/main_test.go), region `test-mail`.)

Each sent email is a `*mailer.Outgoing`: `To`, `Subject`, `HTML`,
`Text`, `Attachments`, … To test a mailable alone, render it with
`m.Render(ctx, mailable)`.

## SMTP settings

`MAIL_SMTP_URL` is the server, with the user and password URL-encoded:

| URL | Connection |
|---|---|
| `smtp://user:pass@smtp.example.com:587` | Upgraded with STARTTLS, which the server must offer (default port 587) |
| `smtps://user:pass@smtp.example.com:465` | TLS from the start (default port 465) |
| `smtp://127.0.0.1:1025` | A local server (127.0.0.1, ::1, localhost): STARTTLS if offered |
| `smtp://relay.internal:25?tls=none` | No TLS, for a relay on a private network; it can't take a password (an error at startup) |

The password is sent only over TLS or to a local server, with AUTH PLAIN
(or LOGIN when the server offers only that). Other parameters:
`timeout=30s` bounds each email (default 30 seconds), and
`local_name=app.example.com` is the name sent in EHLO (default: the
machine's). Each email opens its own connection.

A recipient the server refuses fails the whole email, for every
recipient: permanently for replies in the 500s, except 552 (a full
mailbox), which is retried. Addresses with non-ASCII characters
(`zoë@example.com`) need a server that offers SMTPUTF8.

## How it works

`Send` calls the mailable's `Build`, renders the HTML (and text)
components with the context, generates the text body from the HTML if
there is none, fills in the sender and a `Message-ID`, and checks the
result: valid addresses, at least one recipient, no line breaks in the
subject, names or headers, and no header line over the standard's
length (long values are folded, non-ASCII ones encoded). The rendered
email, a `mailer.Outgoing`, goes to the transport, which checks it
again.

`Queue` does the same, then dispatches the `mail:send` queue job with the
`Outgoing` as its JSON payload; `mailer.ForApp` registers that job when
the app has a queue. The job keeps its `Message-ID` across retries
(Postmark gives each email its own).

SMTP builds the message itself (multipart text and HTML, inline files,
attachments, encoded headers), with the standard library: Bcc recipients
are in the SMTP envelope only. Postmark gets the same fields as JSON.
Transports report errors that retrying won't fix (SMTP replies in the
500s; Postmark's 422, 401, 403 and 413, except "not allowed to send",
error code 405, such as an account out of credits) as permanent, so the
queue fails the job at once.

## Common problems

| Problem | Cause | Fix |
|---|---|---|
| Emails appear in the log instead of being sent | `MAIL_DRIVER` is `log`, the default | Set `MAIL_DRIVER=smtp` (or `postmark`) |
| `the message has no sender` | `MAIL_FROM_ADDRESS` isn't set | Set it, or the message's `From` |
| `URL needs the app's public URL` | `APP_URL` isn't set | Set `APP_URL=https://…` |
| `Queue needs the app's queue` | The app has no queue, or got it after booting | Call `queue.ForApp` in `setup` |
| `doesn't offer SMTPUTF8` | An address with non-ASCII characters, and a server that can't take them | Use the address's ASCII form, or a server with SMTPUTF8 |
| `doesn't offer STARTTLS` | The server has no TLS on that port | Use `smtps://` (port 465), or `tls=none` for a private relay |
| `unencrypted connection` | A password without TLS to a remote server | Use TLS; passwords aren't sent in the clear |
| Links in emails are wrong | `APP_URL` is the development URL in production | Set `APP_URL` per environment |
| The email looks different in Gmail or Outlook | Mail clients drop `<style>` and most CSS | Inline styles, tables or single columns, no scripts |

## Next steps

- [Queues](queues.md): workers and retries, for queued email.
- [Events](events.md): send email from a queued event listener.
- [Configuration reference](../reference/configuration.md#mail): the
  `MAIL_*` settings.

> **Coming from Laravel?** A mailable is a `Mailable` class: `Build`
> returns what `envelope()`, `content()` and `attachments()` do, and the
> view is a templ component instead of a Blade template.
> `mailer.Send(ctx, m)` is `Mail::to(...)->send(m)` (the recipients are
> the mailable's), and `mailer.Queue` is `->queue()`, except that the
> email is rendered when queued. `MAIL_DRIVER` is `MAIL_MAILER`, and
> `MAIL_SMTP_URL` replaces `MAIL_HOST`, `MAIL_PORT`, `MAIL_USERNAME`,
> `MAIL_PASSWORD` and `MAIL_ENCRYPTION`. There are no Markdown mailables
> or notifications yet.
