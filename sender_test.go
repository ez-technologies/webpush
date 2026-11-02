package webpush

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ez-technologies/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeDeliverer struct {
	mu     sync.Mutex
	status map[string]int   // by endpoint suffix
	errs   map[string]error // by endpoint suffix
	got    []Delivery
}

func (f *fakeDeliverer) Deliver(_ context.Context, d Delivery) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, d)
	name := d.Subscription.Endpoint[strings.LastIndex(d.Subscription.Endpoint, "/")+1:]
	if err := f.errs[name]; err != nil {
		return 0, err
	}
	if s, ok := f.status[name]; ok {
		return s, nil
	}
	return http.StatusCreated, nil
}

func newTestSender(t *testing.T, d Deliverer, opts ...Option) (*Sender, *MemoryStore) {
	t.Helper()
	keys, err := GenerateVAPIDKeys()
	require.NoError(t, err)
	store := NewMemoryStore()
	s, err := NewSender(Config{Keys: keys, Subject: "mailto:ops@example.com"}, store, append([]Option{WithDeliverer(d)}, opts...)...)
	require.NoError(t, err)
	return s, store
}

func TestNewSender_RequiresKeysAndStore(t *testing.T) {
	_, err := NewSender(Config{}, NewMemoryStore())
	assert.Equal(t, CodeNotConfigured, apperr.CodeOf(err))

	keys, _ := GenerateVAPIDKeys()
	_, err = NewSender(Config{Keys: keys}, nil)
	assert.Equal(t, CodeNotConfigured, apperr.CodeOf(err))

	s, err := NewSender(Config{Keys: keys}, NewMemoryStore())
	require.NoError(t, err)
	assert.Equal(t, keys.Public, s.PublicKey())
}

func TestSender_SendsToEverySubscriptionAndRemovesGoneOnes(t *testing.T) {
	ctx := context.Background()
	d := &fakeDeliverer{status: map[string]int{"gone": http.StatusGone, "missing": http.StatusNotFound, "busy": http.StatusTooManyRequests}}
	var logged []error
	s, store := newTestSender(t, d, WithErrorHandler(func(err error) { logged = append(logged, err) }))
	for _, e := range []string{"phone", "laptop", "gone", "missing", "busy"} {
		require.NoError(t, store.Save(ctx, sub("u1", e)))
	}
	require.NoError(t, store.Save(ctx, sub("u2", "other")))

	res, err := s.Send(ctx, "u1", Message{Title: "Hello", Body: "World", URL: "/inbox", Tag: "t1"})
	require.NoError(t, err)

	assert.Equal(t, 2, res.Sent)
	assert.Equal(t, 2, res.Removed)
	require.Len(t, res.Failed, 1)
	assert.Contains(t, res.Failed[0].Error(), "429")
	assert.Len(t, logged, 1)
	assert.Len(t, d.got, 5, "u2 is not sent to")

	left, _ := store.ListByUser(ctx, "u1")
	var names []string
	for _, l := range left {
		names = append(names, l.Endpoint[strings.LastIndex(l.Endpoint, "/")+1:])
	}
	assert.ElementsMatch(t, []string{"phone", "laptop", "busy"}, names)

	assert.JSONEq(t, `{"title":"Hello","body":"World","url":"/inbox","tag":"t1"}`, string(d.got[0].Payload))
	assert.Equal(t, 12*time.Hour, d.got[0].TTL)
	assert.Equal(t, UrgencyNormal, d.got[0].Urgency)
}

func TestSender_MessageOverridesDeliveryOptions(t *testing.T) {
	ctx := context.Background()
	d := &fakeDeliverer{}
	s, store := newTestSender(t, d)
	require.NoError(t, store.Save(ctx, sub("u1", "phone")))

	_, err := s.Send(ctx, "u1", Message{Title: "Alarm", TTL: time.Minute, Urgency: UrgencyHigh, Topic: "alarm"})
	require.NoError(t, err)
	assert.Equal(t, time.Minute, d.got[0].TTL)
	assert.Equal(t, UrgencyHigh, d.got[0].Urgency)
	assert.Equal(t, "alarm", d.got[0].Topic)
	assert.NotContains(t, string(d.got[0].Payload), "alarm\"", "delivery options are not in the payload")
}

func TestSender_NoSubscriptionsIsNotAnError(t *testing.T) {
	s, _ := newTestSender(t, &fakeDeliverer{})
	res, err := s.Send(context.Background(), "nobody", Message{Title: "Hi"})
	require.NoError(t, err)
	assert.Equal(t, Result{}, res)
}

func TestSender_AllFailedIsDeliveryFailed(t *testing.T) {
	ctx := context.Background()
	d := &fakeDeliverer{errs: map[string]error{"a": errors.New("dial tcp: refused")}, status: map[string]int{"b": 500}}
	s, store := newTestSender(t, d)
	require.NoError(t, store.Save(ctx, sub("u1", "a")))
	require.NoError(t, store.Save(ctx, sub("u1", "b")))

	res, err := s.Send(ctx, "u1", Message{Title: "Hi"})
	assert.Equal(t, CodeDeliveryFailed, apperr.CodeOf(err))
	assert.Equal(t, 2, apperr.ExtraOf(err)["failed"])
	assert.Len(t, res.Failed, 2)
}

func TestSender_RejectsBadMessages(t *testing.T) {
	s, _ := newTestSender(t, &fakeDeliverer{})
	_, err := s.Send(context.Background(), "u1", Message{Body: "no title"})
	assert.Equal(t, CodeInvalidMessage, apperr.CodeOf(err))

	_, err = s.Send(context.Background(), "u1", Message{Title: "big", Body: strings.Repeat("x", MaxPayloadBytes)})
	assert.Equal(t, CodePayloadTooLarge, apperr.CodeOf(err))
	assert.Equal(t, MaxPayloadBytes, apperr.ExtraOf(err)["limit"])
}

type failingStore struct{ *MemoryStore }

func (f *failingStore) ListByUser(context.Context, string) ([]Subscription, error) {
	return nil, errors.New("db down")
}

func TestSender_StoreErrorHasCode(t *testing.T) {
	keys, _ := GenerateVAPIDKeys()
	s, err := NewSender(Config{Keys: keys}, &failingStore{NewMemoryStore()}, WithDeliverer(&fakeDeliverer{}))
	require.NoError(t, err)
	_, err = s.Send(context.Background(), "u1", Message{Title: "Hi"})
	assert.Equal(t, CodeStore, apperr.CodeOf(err))
}

// browserSubscription makes keys the way a browser does, so the real
// encryption runs.
func browserSubscription(t *testing.T, user, endpoint string) Subscription {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	require.NoError(t, err)
	auth := make([]byte, 16)
	_, _ = rand.Read(auth)
	return Subscription{
		UserID: user, Endpoint: endpoint,
		P256dh: base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()),
		Auth:   base64.RawURLEncoding.EncodeToString(auth),
	}
}

func TestSender_RealDeliveryToAPushService(t *testing.T) {
	type seen struct {
		path, auth, encoding, ttl, urgency, topic string
		size                                      int
	}
	var mu sync.Mutex
	var reqs []seen
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 8192)
		n, _ := r.Body.Read(buf)
		mu.Lock()
		reqs = append(reqs, seen{r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Content-Encoding"),
			r.Header.Get("TTL"), r.Header.Get("Urgency"), r.Header.Get("Topic"), n})
		mu.Unlock()
		if r.URL.Path == "/gone" {
			w.WriteHeader(http.StatusGone)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	keys, _ := GenerateVAPIDKeys()
	store := NewMemoryStore()
	ctx := context.Background()
	require.NoError(t, store.Save(ctx, browserSubscription(t, "u1", srv.URL+"/ok")))
	require.NoError(t, store.Save(ctx, browserSubscription(t, "u1", srv.URL+"/gone")))
	s, err := NewSender(Config{Keys: keys, Subject: "mailto:ops@example.com", TTL: time.Hour, HTTPClient: srv.Client()}, store)
	require.NoError(t, err)

	res, err := s.Send(ctx, "u1", Message{Title: "Hello", Body: "from the test", Urgency: UrgencyHigh, Topic: "greeting"})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Sent)
	assert.Equal(t, 1, res.Removed)

	require.Len(t, reqs, 2)
	r := reqs[0]
	assert.True(t, strings.HasPrefix(r.auth, "vapid t="), r.auth)
	assert.Contains(t, r.auth, "k="+keys.Public)
	assert.Equal(t, "aes128gcm", r.encoding)
	assert.Equal(t, "3600", r.ttl)
	assert.Equal(t, "high", r.urgency)
	assert.Equal(t, "greeting", r.topic)
	assert.Greater(t, r.size, 100, "the body is the encrypted, padded payload")

	left, _ := store.ListByUser(ctx, "u1")
	assert.Len(t, left, 1)
}
