package webpush

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Pusher is what a Throttler sends through. *Sender is one.
type Pusher interface {
	Send(ctx context.Context, userID string, msg Message) (Result, error)
}

// MuteFunc reports whether the user has turned off pushes of this group. It is
// asked before every push the Throttler sends, so a mute takes effect at once,
// also for messages already waiting in a window.
type MuteFunc func(ctx context.Context, userID, group string) bool

// MergeFunc turns the messages that arrived during one window into the single
// message sent when it ends. It is only called with two or more messages.
type MergeFunc func(group string, msgs []Message) Message

// CountMerge is the default MergeFunc: the newest message, with its body
// replaced by "<n> new messages". Use MergeWith for other wording.
func CountMerge(_ string, msgs []Message) Message {
	return MergeWith(func(n int, _ string, _ Message) string { return fmt.Sprintf("%d new messages", n) })("", msgs)
}

// MergeWith builds a MergeFunc that keeps the newest message (its title, URL,
// tag and data) and sets its body to body(count, group, newest). For example
// "3 new messages in Design team".
func MergeWith(body func(n int, group string, newest Message) string) MergeFunc {
	return func(group string, msgs []Message) Message {
		newest := msgs[len(msgs)-1]
		newest.Body = body(len(msgs), group, newest)
		return newest
	}
}

// ThrottleConfig configures a Throttler.
type ThrottleConfig struct {
	// Window is the shortest time between two pushes to the same user and
	// group. Default 2 minutes.
	Window time.Duration
	// Merge combines the messages of one window. Default CountMerge.
	Merge MergeFunc
	// Muted is your mute check. Nil means nothing is muted.
	Muted MuteFunc
	// SendTimeout bounds each push sent when a window ends. Default 30 seconds.
	SendTimeout time.Duration
	// OnError is told about failures of pushes sent when a window ends (there
	// is no caller to return them to). Default: ignored.
	OnError func(userID, group string, err error)
}

// Outcome says what Notify did with a message.
type Outcome int

const (
	// Sent: no window was open, the message went out at once.
	Sent Outcome = iota
	// Queued: a window is open; the message is merged into the push sent when
	// it ends.
	Queued
	// Muted: the mute check said no; nothing is sent.
	Muted
)

func (o Outcome) String() string {
	switch o {
	case Sent:
		return "sent"
	case Queued:
		return "queued"
	case Muted:
		return "muted"
	}
	return "unknown"
}

type throttleKey struct{ user, group string }

type window struct {
	pending []Message
	timer   timer
}

type timer interface{ Stop() bool }

// Throttler sends at most one push per (user, group) per window. The first
// message goes out at once and opens a window; messages arriving while it is
// open are held and, when it ends, sent as one push (merged when there are
// several), which opens the next window. A window with nothing held closes.
//
// Groups are your choice: a conversation, a project, a kind of alert. Pending
// messages live in memory; call Flush on shutdown to send them.
type Throttler struct {
	pusher Pusher
	cfg    ThrottleConfig

	mu      sync.Mutex
	windows map[throttleKey]*window
	closed  bool

	afterFunc func(time.Duration, func()) timer
}

// NewThrottler builds a Throttler in front of a Pusher (usually a *Sender).
func NewThrottler(p Pusher, cfg ThrottleConfig) *Throttler {
	if cfg.Window <= 0 {
		cfg.Window = 2 * time.Minute
	}
	if cfg.Merge == nil {
		cfg.Merge = CountMerge
	}
	if cfg.SendTimeout <= 0 {
		cfg.SendTimeout = 30 * time.Second
	}
	if cfg.OnError == nil {
		cfg.OnError = func(string, string, error) {}
	}
	return &Throttler{
		pusher:    p,
		cfg:       cfg,
		windows:   map[throttleKey]*window{},
		afterFunc: func(d time.Duration, f func()) timer { return time.AfterFunc(d, f) },
	}
}

// Notify sends msg to the user now, or holds it for the end of the open
// window of (userID, group). The error is the Pusher's, for a message sent at
// once; failures of held messages go to ThrottleConfig.OnError.
func (t *Throttler) Notify(ctx context.Context, userID, group string, msg Message) (Outcome, error) {
	if t.muted(ctx, userID, group) {
		return Muted, nil
	}
	k := throttleKey{userID, group}
	t.mu.Lock()
	if w, ok := t.windows[k]; ok {
		w.pending = append(w.pending, msg)
		t.mu.Unlock()
		return Queued, nil
	}
	if !t.closed {
		t.open(k)
	}
	t.mu.Unlock()
	_, err := t.pusher.Send(ctx, userID, msg)
	return Sent, err
}

// Pending is the number of messages held for (userID, group).
func (t *Throttler) Pending(userID, group string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	if w, ok := t.windows[throttleKey{userID, group}]; ok {
		return len(w.pending)
	}
	return 0
}

// Flush sends every held message now (merged per user and group) and closes
// all windows. After Flush, Notify sends at once without throttling; use it on
// shutdown.
func (t *Throttler) Flush(ctx context.Context) {
	t.mu.Lock()
	t.closed = true
	windows := t.windows
	t.windows = map[throttleKey]*window{}
	t.mu.Unlock()
	for k, w := range windows {
		w.timer.Stop()
		if len(w.pending) > 0 {
			t.deliver(ctx, k, w.pending)
		}
	}
}

// open starts a window for k. The caller holds t.mu.
func (t *Throttler) open(k throttleKey) {
	w := &window{}
	t.windows[k] = w
	w.timer = t.afterFunc(t.cfg.Window, func() { t.end(k, w) })
}

func (t *Throttler) end(k throttleKey, w *window) {
	t.mu.Lock()
	if t.windows[k] != w {
		t.mu.Unlock()
		return
	}
	pending := w.pending
	delete(t.windows, k)
	if len(pending) > 0 {
		t.open(k)
	}
	t.mu.Unlock()
	if len(pending) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), t.cfg.SendTimeout)
	defer cancel()
	t.deliver(ctx, k, pending)
}

func (t *Throttler) deliver(ctx context.Context, k throttleKey, pending []Message) {
	if t.muted(ctx, k.user, k.group) {
		return
	}
	msg := pending[0]
	if len(pending) > 1 {
		msg = t.cfg.Merge(k.group, pending)
	}
	if _, err := t.pusher.Send(ctx, k.user, msg); err != nil {
		t.cfg.OnError(k.user, k.group, err)
	}
}

func (t *Throttler) muted(ctx context.Context, userID, group string) bool {
	return t.cfg.Muted != nil && t.cfg.Muted(ctx, userID, group)
}
