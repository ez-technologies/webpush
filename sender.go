package webpush

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	webpushgo "github.com/SherClockHolmes/webpush-go"
	"github.com/ez-technologies/apperr"
)

// Config is the Sender's identity and delivery defaults.
type Config struct {
	// Keys is the VAPID key pair (see GenerateVAPIDKeys).
	Keys VAPIDKeys
	// Subject is how push services can reach you about your traffic: a
	// "mailto:" address or an https URL. Required by some push services.
	Subject string
	// TTL is how long a message waits for an offline device. Default 12 hours.
	TTL time.Duration
	// Urgency is the default urgency. Default UrgencyNormal.
	Urgency Urgency
	// HTTPClient sends the requests. Default an http.Client with a 15 second
	// timeout.
	HTTPClient *http.Client
}

// Delivery is one encrypted push to one subscription.
type Delivery struct {
	Subscription Subscription
	Payload      []byte
	TTL          time.Duration
	Urgency      Urgency
	Topic        string
}

// Deliverer sends one push and returns the push service's HTTP status. The
// default uses the real push services; tests replace it with WithDeliverer.
type Deliverer interface {
	Deliver(ctx context.Context, d Delivery) (status int, err error)
}

// DelivererFunc adapts a function to Deliverer.
type DelivererFunc func(ctx context.Context, d Delivery) (int, error)

// Deliver implements Deliverer.
func (f DelivererFunc) Deliver(ctx context.Context, d Delivery) (int, error) { return f(ctx, d) }

// Result counts what happened to one Send.
type Result struct {
	// Sent is the number of subscriptions the push service accepted.
	Sent int
	// Removed is the number of subscriptions reported gone (404/410) and
	// deleted from the store.
	Removed int
	// Failed lists the other failures, one per subscription.
	Failed []error
}

// Sender delivers messages to every subscription of a user.
type Sender struct {
	cfg       Config
	store     Store
	deliverer Deliverer
	onError   func(error)
}

// Option changes a Sender.
type Option func(*Sender)

// WithDeliverer replaces the delivery to the push services, for tests.
func WithDeliverer(d Deliverer) Option { return func(s *Sender) { s.deliverer = d } }

// WithErrorHandler is told about failures that Send reports in Result.Failed
// and about store errors while removing gone subscriptions, for logging.
func WithErrorHandler(f func(error)) Option { return func(s *Sender) { s.onError = f } }

// NewSender checks the configuration and builds a Sender.
func NewSender(cfg Config, store Store, opts ...Option) (*Sender, error) {
	if err := cfg.Keys.Validate(); err != nil {
		return nil, err
	}
	if store == nil {
		return nil, apperr.New(CodeNotConfigured, "webpush: a Store is required")
	}
	if cfg.TTL <= 0 {
		cfg.TTL = 12 * time.Hour
	}
	if cfg.Urgency == "" {
		cfg.Urgency = UrgencyNormal
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	s := &Sender{cfg: cfg, store: store, onError: func(error) {}}
	s.deliverer = webDeliverer{cfg: cfg}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

// PublicKey is the VAPID public key browsers subscribe with.
func (s *Sender) PublicKey() string { return s.cfg.Keys.Public }

// Store is the store the Sender reads subscriptions from.
func (s *Sender) Store() Store { return s.store }

// Send delivers msg to every subscription of the user and removes the ones the
// push service reports as gone. A user with no subscriptions is not an error
// (Result.Sent is 0). The error is non-nil when the message is invalid, the
// store fails, or no subscription accepted it while at least one failed; in
// the last case it has code CodeDeliveryFailed and Result.Failed has details.
func (s *Sender) Send(ctx context.Context, userID string, msg Message) (Result, error) {
	payload, err := msg.Payload()
	if err != nil {
		return Result{}, err
	}
	subs, err := s.store.ListByUser(ctx, userID)
	if err != nil {
		return Result{}, apperr.Wrap(CodeStore, err)
	}
	d := Delivery{Payload: payload, TTL: s.cfg.TTL, Urgency: s.cfg.Urgency, Topic: msg.Topic}
	if msg.TTL > 0 {
		d.TTL = msg.TTL
	}
	if msg.Urgency != "" {
		d.Urgency = msg.Urgency
	}
	var res Result
	for _, sub := range subs {
		d.Subscription = sub
		status, err := s.deliverer.Deliver(ctx, d)
		switch {
		case err != nil:
			res.Failed = append(res.Failed, fmt.Errorf("webpush: %s: %w", shortEndpoint(sub.Endpoint), err))
		case status == http.StatusNotFound || status == http.StatusGone:
			if err := s.store.Delete(ctx, sub.UserID, sub.Endpoint); err != nil {
				s.onError(apperr.Wrap(CodeStore, err))
			} else {
				res.Removed++
			}
		case status >= 200 && status < 300:
			res.Sent++
		default:
			res.Failed = append(res.Failed, fmt.Errorf("webpush: %s: push service answered %d", shortEndpoint(sub.Endpoint), status))
		}
	}
	for _, e := range res.Failed {
		s.onError(e)
	}
	if res.Sent == 0 && len(res.Failed) > 0 {
		return res, apperr.Wrap(CodeDeliveryFailed, errors.Join(res.Failed...)).With("failed", len(res.Failed))
	}
	return res, nil
}

// shortEndpoint keeps logs readable and free of the full capability URL.
func shortEndpoint(endpoint string) string {
	if len(endpoint) > 48 {
		return endpoint[:48] + "..."
	}
	return endpoint
}

type webDeliverer struct{ cfg Config }

func (w webDeliverer) Deliver(ctx context.Context, d Delivery) (int, error) {
	res, err := webpushgo.SendNotificationWithContext(ctx, d.Payload, &webpushgo.Subscription{
		Endpoint: d.Subscription.Endpoint,
		Keys:     webpushgo.Keys{Auth: d.Subscription.Auth, P256dh: d.Subscription.P256dh},
	}, &webpushgo.Options{
		HTTPClient:      w.cfg.HTTPClient,
		Subscriber:      w.cfg.Subject,
		Topic:           d.Topic,
		TTL:             int(d.TTL / time.Second),
		Urgency:         webpushgo.Urgency(d.Urgency),
		VAPIDPublicKey:  w.cfg.Keys.Public,
		VAPIDPrivateKey: w.cfg.Keys.Private,
	})
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
	return res.StatusCode, nil
}
