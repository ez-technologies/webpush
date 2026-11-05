# webpush

**Web Push notifications for web apps and PWAs: a Go sender and the browser side, in one small library.**

- **Go**: VAPID keys, a subscription store (in memory or PostgreSQL through GORM), a sender that reaches every
  device of a user and forgets dead subscriptions, a throttle that merges bursts ("3 new messages in Design team"),
  a mute hook, and Gin handlers for the three endpoints the browser needs.
- **Browser** (TypeScript, no dependencies): `subscribe()` / `unsubscribe()`, permission and iOS helpers, and a
  service worker that shows the notification and focuses or opens your app when it is tapped.
- Errors carry stable codes ([apperr](https://github.com/ez-technologies/apperr)) on both sides, so your UI can
  say the right thing.

| The demo, after a test push and a merged burst | On a phone |
| --- | --- |
| ![The demo page: subscribed, with a test push, the first chat message and the merged push received](docs/images/demo-merged.png) | ![The same page at phone width](docs/images/demo-phone.png) |

![Two mock phone lock screens: the first chat message sent at once, then the next three merged into "3 new messages in Design team"](docs/images/notification-mock.png)

*Mock lock screens: these are the real payloads the demo received in headless Chromium, drawn as phone
notifications (a system notification cannot be captured in a headless browser).*

## Contents

- [Install](#install)
- [Quick start](#quick-start)
- [How to](#how-to)
  - [Generate VAPID keys](#generate-vapid-keys)
  - [Store subscriptions](#store-subscriptions)
  - [Send to a user](#send-to-a-user)
  - [Throttle and merge per group](#throttle-and-merge-per-group)
  - [Mute](#mute)
  - [Set up the service worker](#set-up-the-service-worker)
  - [Subscribe in the browser](#subscribe-in-the-browser)
  - [iOS and Android notes](#ios-and-android-notes)
- [Options](#options)
- [Error codes](#error-codes)
- [Run the demo](#run-the-demo)
- [Development](#development)

## Install

Go (1.26 or later):

```sh
go get github.com/ez-technologies/webpush@latest
```

Browser package, straight from GitHub (it builds itself on install):

```sh
npm install "github:ez-technologies/webpush#v0.1.0"
```

Installing from Git runs the package's `prepare` script, which compiles the TypeScript; newer npm versions list it
under `install-scripts` the first time.

```ts
import { subscribe, unsubscribe, pushStatus } from '@ez-technologies/webpush'
import { installPushHandlers } from '@ez-technologies/webpush/sw' // in a bundled service worker
```

## Quick start

**1. Make a key pair once** and keep it in your configuration:

```sh
go run github.com/ez-technologies/webpush/cmd/vapidkeys@latest
# VAPID_PUBLIC_KEY=BK...
# VAPID_PRIVATE_KEY=x2...
```

**2. Server** (Gin and PostgreSQL here; both are optional):

```go
import (
	"github.com/ez-technologies/webpush"
	"github.com/ez-technologies/webpush/ginpush"
	"github.com/ez-technologies/webpush/gormstore"
)

store := gormstore.New(db) // *gorm.DB
if err := store.Migrate(ctx); err != nil { ... }

sender, err := webpush.NewSender(webpush.Config{
	Keys:    webpush.VAPIDKeys{Public: os.Getenv("VAPID_PUBLIC_KEY"), Private: os.Getenv("VAPID_PRIVATE_KEY")},
	Subject: "mailto:ops@example.com",
}, store)
if err != nil { ... }

// GET /api/push/vapid-public-key, POST and DELETE /api/push/subscriptions
ginpush.New(store, sender.PublicKey(), func(c *gin.Context) (string, bool) {
	id := c.GetString("userID") // from your login middleware
	return id, id != ""
}).Register(router.Group("/api/push", requireLogin))

// Anywhere in your code:
res, err := sender.Send(ctx, userID, webpush.Message{
	Title: "Your order shipped",
	Body:  "It arrives on Thursday.",
	URL:   "/orders/1042",
	Tag:   "order-1042",
})
```

**3. Service worker**: serve `node_modules/@ez-technologies/webpush/client/dist/push-sw.js` as `/push-sw.js`, or
add two lines to the service worker you already have (see [below](#set-up-the-service-worker)).

**4. Page**: call `subscribe()` from a button:

```ts
import { subscribe, WebPushError } from '@ez-technologies/webpush'

button.onclick = async () => {
  try {
    await subscribe({ serviceWorkerUrl: '/push-sw.js' })
  } catch (err) {
    if (err instanceof WebPushError && err.code === 'needs-home-screen') showInstallHint()
  }
}
```

## How to

### Generate VAPID keys

VAPID keys identify your server to the push services (Google, Mozilla, Apple, Microsoft). Generate them **once**:
browsers subscribe with the public key, so a new pair means every browser has to subscribe again.

```sh
go run github.com/ez-technologies/webpush/cmd/vapidkeys@latest                    # VAPID_PUBLIC_KEY=... lines
go run github.com/ez-technologies/webpush/cmd/vapidkeys@latest -prefix MYAPP_VAPID_
go run github.com/ez-technologies/webpush/cmd/vapidkeys@latest -format json
```

or in code:

```go
keys, err := webpush.GenerateVAPIDKeys() // keys.Public, keys.Private (base64url)
err = keys.Validate()                     // both present, P-256, and they belong together
```

Keep the private key out of your repository (an environment variable or a secret store). `NewSender` validates the
pair and refuses a mismatched one.

### Store subscriptions

A `webpush.Subscription` is one browser's address for pushes, tied to one of your users. A `Store` keeps them:

```go
type Store interface {
	Save(ctx context.Context, sub Subscription) error                       // insert, or replace by endpoint
	Delete(ctx context.Context, userID, endpoint string) error              // only the user's own; no error if absent
	ListByUser(ctx context.Context, userID string) ([]Subscription, error)  // oldest first
}
```

- `webpush.NewMemoryStore()`: in memory, for tests, demos and single-process apps.
- `gormstore.New(db)`: a table `webpush_subscriptions` through GORM, written for PostgreSQL (SQLite works too).
  `Migrate(ctx)` creates it; or create it with your own migrations from `gormstore.Record`. Pick another table name
  with `gormstore.New(db, gormstore.Options{Table: "push_subscriptions"})`.
- Your own: implement the three methods. Make `Save` an upsert on the endpoint: the same browser signing in as
  another user moves its subscription.

The Gin handlers save and delete the calling user's subscription. Without Gin, call `store.Save` with the body of
`PushSubscription.toJSON()` yourself.

### Send to a user

```go
res, err := sender.Send(ctx, userID, webpush.Message{
	Title: "Build finished",
	Body:  "main is green again.",
	URL:   "/builds/381",            // opened or focused on tap
	Tag:   "build",                  // a newer "build" notification replaces the older one
	Data:  map[string]any{"id": 381}, // passed to the service worker as is
	Urgency: webpush.UrgencyHigh,     // delivery options: Urgency, TTL, Topic
})
// res.Sent: subscriptions the push service accepted
// res.Removed: subscriptions that answered 404/410 and were deleted from the store
// res.Failed: other failures, one error each
```

`Send` is synchronous and sends to every subscription of the user. A user with no subscriptions is not an error.
`err` is set when the message is invalid (`webpush_invalid_message`, `webpush_payload_too_large`), the store fails,
or no subscription accepted it (`webpush_delivery_failed`). To send without waiting, call it in a goroutine with
its own context.

To word the message for each device, use `SendEach`. It asks for a message per subscription, so there is no query
per language, and a message that is not valid fails only that subscription:

```go
res, err := sender.SendEach(ctx, userID, func(sub webpush.Subscription) webpush.Message {
	return webpush.Message{Title: titleIn(sub.Lang), URL: "/builds/381"}
})
```

### Move to this library from your own push code

People already subscribed can stay subscribed. Keep the VAPID keys, the service worker's address and its scope, and
implement `Store` over the table you have. Three things cover the time until every device has your new worker:

- **Devices still on your old worker.** `Message.Extra` adds top-level fields to the payload, so the old worker finds
  the names it reads: `Extra: map[string]any{"link": "/builds/381"}`.
- **Notifications your old worker showed or left on their way.** `urlFields: ['url', 'link']` makes the new worker read
  the old name as well.
- **Keys the check refuses.** `NewSender` validates the keys, and your old code may not have. With
  `WithInvalidKeysHandler(f)`, keys it refuses are reported to `f` and used as given, so sending goes on as before.

### Throttle and merge per group

A chat that sends a push per message buzzes a phone ten times a minute. A `Throttler` sends **at most one push per
(user, group) per window**: the first message goes out at once and opens the window; the ones that arrive while it
is open are held and sent as **one** push when it ends ("3 new messages in Design team"), which opens the next
window. A window that ends with nothing held closes, so the next message goes out at once again.

```go
throttler := webpush.NewThrottler(sender, webpush.ThrottleConfig{
	Window: 2 * time.Minute,
	Merge: webpush.MergeWith(func(n int, group string, newest webpush.Message) string {
		return fmt.Sprintf("%d new messages in %s", n, newest.Title)
	}),
	Muted:   isMuted, // see Mute
	OnError: func(userID, group string, err error) { log.Print(err) },
})

outcome, err := throttler.Notify(ctx, userID, "chat:"+roomID, webpush.Message{
	Title: roomName, Body: text, URL: "/chat/" + roomID, Tag: "chat-" + roomID,
})
// outcome: webpush.Sent, webpush.Queued or webpush.Muted
```

- The group is yours to choose: a conversation, a project, a kind of alert. Groups and users are independent.
- The merged push is the newest message with its body replaced, so its title, URL, tag and data are the newest
  ones. Use the same `Tag` for a group and the merged push **replaces** the earlier notification on the device
  instead of stacking. `CountMerge` (the default) says "3 new messages"; write any `MergeFunc` for more.
- Held messages live in memory. Call `throttler.Flush(ctx)` on shutdown to send them; after `Flush`, `Notify`
  sends at once. With several server instances, each throttles on its own.

### Mute

The Throttler asks your mute check before every push, both when a message arrives and when a window ends, so a mute
takes effect at once, also for messages already held:

```go
isMuted := func(ctx context.Context, userID, group string) bool {
	return settings.PushMuted(ctx, userID, group) // your table, your rules: per group, quiet hours, ...
}
```

`Sender.Send` itself never mutes: it is the "send this now" path (for example a password reset confirmation).

### Set up the service worker

The worker shows the notification (title, body, icon, badge, tag) and, on tap, focuses a window already at the URL,
or focuses and navigates an open window of your app, or opens a new one.

**No service worker yet**: serve the ready-made classic script and register it (`subscribe` does that for you with
`serviceWorkerUrl`):

```go
r.StaticFile("/push-sw.js", "node_modules/@ez-technologies/webpush/client/dist/push-sw.js")
```

Options go in the URL: `/push-sw.js?title=My%20app&icon=/icon-192.png&badge=/badge-72.png&url=/inbox`
(`skipWhenFocused=1`, `click=message` too).

**Already have one** (for example from a PWA plugin), add it to that one, either with `importScripts`:

```js
self.WEBPUSH_OPTIONS = { defaultTitle: 'My app', icon: '/icon-192.png' }
importScripts('/push-sw.js')
```

or, in a bundled worker:

```ts
import { installPushHandlers } from '@ez-technologies/webpush/sw'
installPushHandlers(self as any, { defaultTitle: 'My app', icon: '/icon-192.png', badge: '/badge-72.png' })
```

The worker also posts `{type: 'webpush:push', payload}` to your open windows on every push, so a page can update
its own badge or list:

```ts
navigator.serviceWorker.addEventListener('message', (e) => {
  if (e.data?.type === 'webpush:push') refreshInbox(e.data.payload)
})
```

With `clickBehavior: 'message'` a tap focuses the open window and posts `{type: 'webpush:click', url}` instead of
navigating, for single-page apps that route themselves. Lower-level pieces (`handlePush`, `openOrFocus`,
`parsePushData`) are exported for workers that need their own `push` listener.

### Subscribe in the browser

```ts
import { subscribe, unsubscribe, pushStatus, permission, needsHomeScreenInstall } from '@ez-technologies/webpush'

// What to offer: 'unsupported' | 'needs-home-screen' | 'denied' | 'subscribed' | 'not-subscribed'
const status = await pushStatus()

await subscribe({ lang: 'en', fetchInit: { headers: { 'X-CSRF-Token': token } } })
await unsubscribe()
```

`subscribe` asks permission (only if not decided yet), registers `serviceWorkerUrl` if the page has no worker, waits
for it, subscribes with the server's public key (fetched from `publicKeyUrl` unless you pass `publicKey`), and posts
the subscription to `subscriptionsUrl`. Calling it again is safe: an existing subscription is reused, or replaced
when it was made with another key. **Call it from a click**: browsers block or quietly ignore permission prompts that
are not.

Failures reject with a `WebPushError` whose `code` is one of `unsupported`, `needs-home-screen`,
`permission-denied`, `permission-dismissed`, `no-service-worker`, `server` (with `status`) or `subscribe-failed`.

`unsubscribe` tells the server and unsubscribes the browser (the browser part happens even when the server call
fails; the server forgets the endpoint at the next push anyway, when the push service answers 410).

### iOS and Android notes

- **iPhone and iPad (iOS/iPadOS 16.4 and later)**: Web Push works **only for a site added to the Home Screen** and
  opened from there; in Safari itself there is no `PushManager`. `needsHomeScreenInstall()` tells you when to show
  "Tap Share, then Add to Home Screen", and `subscribe` rejects with `needs-home-screen` in that case. The site needs
  a web app manifest with `"display": "standalone"` (or `fullscreen`). iPadOS reports itself as a Mac; the helper
  checks for a touch screen too.
- On iOS, every push **must** show a notification (`userVisibleOnly`); the service worker here always does, unless
  you set `skipWhenFocused` and the app is in front.
- **Android (Chrome, Edge, Firefox, Samsung Internet)**: push works in the browser and in installed PWAs, also when
  the browser is closed. The `badge` is a small monochrome icon in the status bar; give one, or Android shows a
  generic bell.
- **Desktop**: Chrome, Edge, Firefox and Safari (macOS 13+) all work; notifications appear only while the browser is
  running.
- Push needs a secure context: https, or `http://localhost` while developing.
- Private/incognito windows in Chrome do not support push.

## Options

### Go

| Setting | Default | Meaning |
| --- | --- | --- |
| `Config.Keys` | (required) | VAPID key pair. |
| `Config.Subject` | empty | `mailto:` address or https URL the push services can contact you at. Set it: Apple's push service rejects pushes without one. |
| `Config.TTL` | 12h | How long a push service keeps a message for an offline device. |
| `Config.Urgency` | `normal` | `very-low`, `low`, `normal` or `high`. |
| `Config.HTTPClient` | 15s timeout | Client for the push service requests. |
| `WithErrorHandler(f)` | ignore | Told about each failed delivery and store errors while removing gone subscriptions. |
| `WithDeliverer(d)` | real push services | Replace delivery, for tests. |
| `WithInvalidKeysHandler(f)` | refuse | Keys that fail validation are reported to `f` and used as given. Missing keys are still refused. |
| `Message.Extra` | none | Top-level payload fields of your own, for a service worker that reads other names. The standard fields win. |
| `Message.TTL`, `.Urgency`, `.Topic` | from Config | Per-message delivery options. `Topic` lets the push service replace an undelivered message with the same topic. |
| `ThrottleConfig.Window` | 2m | Shortest time between two pushes to the same user and group. |
| `ThrottleConfig.Merge` | `CountMerge` | Turns the held messages into one ("3 new messages"). `MergeWith` builds one from a body function. |
| `ThrottleConfig.Muted` | nothing muted | `func(ctx, userID, group) bool`, asked before every push. |
| `ThrottleConfig.SendTimeout` | 30s | Bound on each push sent when a window ends. |
| `ThrottleConfig.OnError` | ignore | Errors of pushes sent when a window ends. |
| `gormstore.Options.Table` | `webpush_subscriptions` | Table name. |

### Browser (`subscribe`, `unsubscribe`, `pushStatus`)

| Option | Default | Meaning |
| --- | --- | --- |
| `publicKey` | fetched | VAPID public key; skips the request to `publicKeyUrl`. |
| `publicKeyUrl` | `/api/push/vapid-public-key` | Answers `{"publicKey": "..."}`. |
| `subscriptionsUrl` | `/api/push/subscriptions` | POST saves, DELETE removes. |
| `serviceWorkerUrl` | none | Registered when the page has no service worker. |
| `scope` | browser default | Scope of the service worker. |
| `lang` | `navigator.language` | Stored with the subscription, to word messages per device. |
| `fetchInit` | `credentials: 'same-origin'` | Merged into every request (headers, credentials). |
| `readyTimeoutMs` | 10000 | How long to wait for the service worker to register and activate. |
| `checkResponse` | a `WebPushError` with code `server` | `(res) => ...` replaces the status check on saving and removing: throw your own error to show the server's refusal in your app's words. |

### Service worker (`installPushHandlers`, or `push-sw.js` query / `self.WEBPUSH_OPTIONS`)

| Option | Query | Default | Meaning |
| --- | --- | --- | --- |
| `defaultTitle` | `title` | `New notification` | Title for a push without one. |
| `defaultUrl` | `url` | `/` | Opened for a push without a URL. |
| `icon`, `badge` | `icon`, `badge` | none | Default images. |
| `skipWhenFocused` | `skipWhenFocused=1` | false | No notification while the app is visible and focused (except `requireInteraction`). |
| `notifyClients` | | true | Post `webpush:push` messages to open windows. |
| `clickBehavior` | `click` | `navigate` | `navigate` an open window, or `message` it. |
| `urlFields` | | `['url']` | Fields to read the URL from, in payloads and in notifications on screen, first one found. |

The payload the worker reads is `webpush.Message` as JSON: `title`, `body`, `url`, `tag`, `icon`, `badge`,
`requireInteraction`, `data`. Pushes from other senders work too; plain text becomes the body.

## Error codes

Go errors carry a code; read it with `apperr.CodeOf(err)`. The Gin handlers answer
`{"error": "...", "code": "..."}`.

| Code | When |
| --- | --- |
| `webpush_not_configured` | No VAPID keys, or no store. |
| `webpush_invalid_keys` | A key is not base64url P-256, or the pair does not match. |
| `webpush_invalid_subscription` | No user, a non-https endpoint, or missing keys (HTTP 400). |
| `webpush_invalid_message` | No title, or `Data` that cannot be encoded. |
| `webpush_payload_too_large` | Encoded message over `MaxPayloadBytes` (3900); `size` and `limit` in the extras. |
| `webpush_delivery_failed` | No subscription of the user accepted the push; `failed` count in the extras. |
| `webpush_store_failed` | The store returned an error (HTTP 500; the cause is not shown to the client). |
| `webpush_unauthenticated` | The handlers' user function said nobody is signed in (HTTP 401). |

## Run the demo

The demo is a Go server and one page: subscribe, send a test push, and send four chat messages to see the throttle
send the first at once and merge the rest.

```sh
npm install && npm run build       # builds client/dist
go run ./examples/demo             # http://localhost:8080
go run ./examples/demo -window 10s # shorter merge window
```

It generates a VAPID key pair at every start (set `VAPID_PUBLIC_KEY` and `VAPID_PRIVATE_KEY` to keep one), keeps
subscriptions in memory, and gives each browser its own demo user in a cookie.

| Before subscribing | Three messages held, one sent |
| --- | --- |
| ![The demo before subscribing: permission default, not subscribed](docs/images/demo-start.png) | ![After sending four chat messages: one sent now, three held](docs/images/demo-burst-waiting.png) |

The screenshots come from the demo running in headless Chromium, with real pushes through the browser's push
service.

## Development

```sh
gofmt -l . && go vet ./... && go test ./...
WEBPUSH_TEST_POSTGRES_URL=postgres://postgres:test@localhost:5432/webpush?sslmode=disable go test ./gormstore
npm install && npm run check && npm test
```

The store tests run on SQLite in memory always, and on PostgreSQL when `WEBPUSH_TEST_POSTGRES_URL` is set.

## License

MIT
