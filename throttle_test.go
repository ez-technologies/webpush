package webpush

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type sent struct {
	user string
	msg  Message
}

type fakePusher struct {
	mu   sync.Mutex
	sent []sent
	err  error
}

func (f *fakePusher) Send(_ context.Context, user string, msg Message) (Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sent{user, msg})
	return Result{Sent: 1}, f.err
}

func (f *fakePusher) bodies() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, s := range f.sent {
		out = append(out, s.user+": "+s.msg.Body)
	}
	return out
}

// fakeTimers runs window ends when the test says so.
type fakeTimers struct {
	mu      sync.Mutex
	pending []*fakeTimer
	windows []time.Duration
}

type fakeTimer struct {
	f       func()
	stopped bool
}

func (t *fakeTimer) Stop() bool { t.stopped = true; return true }

func (ft *fakeTimers) afterFunc(d time.Duration, f func()) timer {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	t := &fakeTimer{f: f}
	ft.pending = append(ft.pending, t)
	ft.windows = append(ft.windows, d)
	return t
}

// fire ends every window open now (not the ones those ends open).
func (ft *fakeTimers) fire() {
	ft.mu.Lock()
	due := ft.pending
	ft.pending = nil
	ft.mu.Unlock()
	for _, t := range due {
		if !t.stopped {
			t.f()
		}
	}
}

func newTestThrottler(cfg ThrottleConfig) (*Throttler, *fakePusher, *fakeTimers) {
	p := &fakePusher{}
	ft := &fakeTimers{}
	th := NewThrottler(p, cfg)
	th.afterFunc = ft.afterFunc
	return th, p, ft
}

func TestThrottler_FirstGoesAtOnceTheRestAreMergedAtWindowEnd(t *testing.T) {
	ctx := context.Background()
	th, p, ft := newTestThrottler(ThrottleConfig{})

	out, err := th.Notify(ctx, "u1", "chat", Message{Title: "Design team", Body: "first"})
	require.NoError(t, err)
	assert.Equal(t, Sent, out)
	for _, b := range []string{"second", "third", "fourth"} {
		out, _ = th.Notify(ctx, "u1", "chat", Message{Title: "Design team", Body: b, URL: "/chat/" + b})
		assert.Equal(t, Queued, out)
	}
	assert.Equal(t, 3, th.Pending("u1", "chat"))
	assert.Equal(t, []string{"u1: first"}, p.bodies())
	assert.Equal(t, 2*time.Minute, ft.windows[0], "default window")

	ft.fire()
	assert.Equal(t, []string{"u1: first", "u1: 3 new messages"}, p.bodies())
	assert.Equal(t, "/chat/fourth", p.sent[1].msg.URL, "the newest message's link")
	assert.Equal(t, "Design team", p.sent[1].msg.Title)

	// The merged push opened a new window: the next message waits again.
	out, _ = th.Notify(ctx, "u1", "chat", Message{Title: "Design team", Body: "fifth"})
	assert.Equal(t, Queued, out)
	ft.fire()
	assert.Equal(t, "u1: fifth", p.bodies()[2], "a single held message is sent as it is")

	// A window that ends with nothing held closes; the next message goes at once.
	ft.fire()
	out, _ = th.Notify(ctx, "u1", "chat", Message{Title: "Design team", Body: "sixth"})
	assert.Equal(t, Sent, out)
	assert.Len(t, p.sent, 4)
}

func TestThrottler_GroupsAndUsersAreIndependent(t *testing.T) {
	ctx := context.Background()
	th, p, _ := newTestThrottler(ThrottleConfig{Window: 10 * time.Second})

	for _, k := range [][2]string{{"u1", "chat"}, {"u1", "alerts"}, {"u2", "chat"}, {"u1", "chat"}} {
		_, _ = th.Notify(ctx, k[0], k[1], Message{Title: "t", Body: k[1]})
	}
	assert.Len(t, p.sent, 3)
	assert.Equal(t, 1, th.Pending("u1", "chat"))
	assert.Equal(t, 0, th.Pending("u2", "chat"))
}

func TestThrottler_CustomMerge(t *testing.T) {
	ctx := context.Background()
	th, p, ft := newTestThrottler(ThrottleConfig{
		Merge: MergeWith(func(n int, group string, newest Message) string {
			return fmt.Sprintf("%d new messages in %s", n, newest.Title)
		}),
	})
	for i := 0; i < 4; i++ {
		_, _ = th.Notify(ctx, "u1", "room-7", Message{Title: "Design team", Body: "hi"})
	}
	ft.fire()
	assert.Equal(t, "u1: 3 new messages in Design team", p.bodies()[1])
}

func TestThrottler_MuteIsCheckedOnArrivalAndAtWindowEnd(t *testing.T) {
	ctx := context.Background()
	var mu sync.Mutex
	muted := map[string]bool{"alerts": true}
	th, p, ft := newTestThrottler(ThrottleConfig{Muted: func(_ context.Context, user, group string) bool {
		mu.Lock()
		defer mu.Unlock()
		return muted[group]
	}})

	out, _ := th.Notify(ctx, "u1", "alerts", Message{Title: "t"})
	assert.Equal(t, Muted, out)
	assert.Empty(t, p.sent)

	_, _ = th.Notify(ctx, "u1", "chat", Message{Title: "t", Body: "a"})
	_, _ = th.Notify(ctx, "u1", "chat", Message{Title: "t", Body: "b"})
	mu.Lock()
	muted["chat"] = true
	mu.Unlock()
	ft.fire()
	assert.Len(t, p.sent, 1, "muted during the window: the held message is dropped")
}

func TestThrottler_ErrorsOfHeldMessagesGoToOnError(t *testing.T) {
	ctx := context.Background()
	var got []string
	th, p, ft := newTestThrottler(ThrottleConfig{OnError: func(user, group string, err error) {
		got = append(got, user+"/"+group+": "+err.Error())
	}})
	p.err = errors.New("push service down")

	_, err := th.Notify(ctx, "u1", "chat", Message{Title: "t"})
	assert.EqualError(t, err, "push service down", "an immediate send returns its error")
	_, _ = th.Notify(ctx, "u1", "chat", Message{Title: "t"})
	ft.fire()
	assert.Equal(t, []string{"u1/chat: push service down"}, got)
}

func TestThrottler_FlushSendsHeldMessagesAndStopsThrottling(t *testing.T) {
	ctx := context.Background()
	th, p, ft := newTestThrottler(ThrottleConfig{})
	_, _ = th.Notify(ctx, "u1", "chat", Message{Title: "t", Body: "a"})
	_, _ = th.Notify(ctx, "u1", "chat", Message{Title: "t", Body: "b"})
	_, _ = th.Notify(ctx, "u1", "chat", Message{Title: "t", Body: "c"})

	th.Flush(ctx)
	assert.Equal(t, []string{"u1: a", "u1: 2 new messages"}, p.bodies())
	ft.fire() // the stopped timer does not send again
	assert.Len(t, p.sent, 2)

	out, _ := th.Notify(ctx, "u1", "chat", Message{Title: "t", Body: "d"})
	assert.Equal(t, Sent, out)
	out, _ = th.Notify(ctx, "u1", "chat", Message{Title: "t", Body: "e"})
	assert.Equal(t, Sent, out)
}

func TestThrottler_RealTimers(t *testing.T) {
	p := &fakePusher{}
	th := NewThrottler(p, ThrottleConfig{Window: 30 * time.Millisecond})
	ctx := context.Background()
	_, _ = th.Notify(ctx, "u1", "chat", Message{Title: "t", Body: "a"})
	_, _ = th.Notify(ctx, "u1", "chat", Message{Title: "t", Body: "b"})
	_, _ = th.Notify(ctx, "u1", "chat", Message{Title: "t", Body: "c"})
	assert.Eventually(t, func() bool { return len(p.bodies()) == 2 }, 2*time.Second, 5*time.Millisecond)
	assert.Equal(t, "u1: 2 new messages", p.bodies()[1])
}

func TestOutcome_String(t *testing.T) {
	assert.Equal(t, "sent", Sent.String())
	assert.Equal(t, "queued", Queued.String())
	assert.Equal(t, "muted", Muted.String())
	assert.Equal(t, "unknown", Outcome(9).String())
}
