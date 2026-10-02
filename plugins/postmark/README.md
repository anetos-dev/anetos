# Postmark plugin

Sends your app's email through [Postmark](https://postmarkapp.com), and
keeps a list of the addresses Postmark stopped sending to (hard bounces,
spam complaints, unsubscribes), so your app can stop emailing them too.

## Install

From your project's directory:

```bash
go tool anetos add anetos.dev/anetos/plugins/postmark
go run . migrate
```

It adds:

| | |
|---|---|
| Settings | `POSTMARK_WEBHOOK_USER`, `POSTMARK_WEBHOOK_PASSWORD`: the basic auth of the webhook URL |
| Route | `POST /postmark/webhook` (`postmark.webhook`), which queues each event |
| Job | `postmark:webhook`, run by the app's queue workers, which updates the list |
| Table | `postmark_suppressions` (migration set `postmark`) |
| Commands | `postmark:suppressions` (list), `postmark:unsuppress <email>` |

`go run . plugins:list` shows them.

## Send email through Postmark

The mail transport is a mailer driver, which your `setup` passes to
`mailer.ForApp` (installing the plugin doesn't change your mailer):

```go
// illustrative
m, err := mailer.ForApp(app, postmark.Driver())
```

```env
MAIL_DRIVER=postmark
MAIL_POSTMARK_TOKEN=…          # the server API token
MAIL_POSTMARK_STREAM=outbound  # the default
```

## Receive Postmark's webhooks

In Postmark, add a webhook to
`https://USER:PASSWORD@example.com/postmark/webhook` for the Bounce,
Spam Complaint and Subscription Change events, with the user and
password of your `POSTMARK_WEBHOOK_*` settings. Without them, the
endpoint refuses every request. Then check an address before emailing
it with `postmark.Suppressed(ctx, email)`.

## More

- [Plugins](../../docs/site/guides/plugins.md): installing and removing plugins
- [Send email](../../docs/site/guides/mail.md): the mailer
- `go doc anetos.dev/anetos/plugins/postmark`
