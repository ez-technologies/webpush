package webpush

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ez-technologies/apperr"
)

// MaxPayloadBytes is the largest encoded message the Sender accepts. Push
// services take about 4 KB after encryption; this leaves room for it.
const MaxPayloadBytes = 3900

// Urgency tells the push service how soon the device should be woken.
type Urgency string

// Urgency values, from RFC 8030.
const (
	UrgencyVeryLow Urgency = "very-low"
	UrgencyLow     Urgency = "low"
	UrgencyNormal  Urgency = "normal"
	UrgencyHigh    Urgency = "high"
)

// Message is what the device shows. Title, Body, URL, Tag and Data are sent
// to the service worker as JSON; TTL, Urgency and Topic are delivery options
// for the push service and are not part of the payload.
type Message struct {
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
	// URL is opened (or focused, when already open) when the user taps the
	// notification. Relative URLs are resolved against the service worker's
	// scope.
	URL string `json:"url,omitempty"`
	// Tag makes a newer notification with the same tag replace the older one
	// on the device instead of stacking.
	Tag string `json:"tag,omitempty"`
	// Icon and Badge are image URLs; empty uses the service worker's defaults.
	Icon  string `json:"icon,omitempty"`
	Badge string `json:"badge,omitempty"`
	// RequireInteraction keeps the notification on screen until the user acts.
	RequireInteraction bool `json:"requireInteraction,omitempty"`
	// Data is passed through to the service worker untouched.
	Data map[string]any `json:"data,omitempty"`
	// Extra adds fields at the top level of the payload, for a service worker
	// that reads names of its own, such as an older version of yours still
	// installed on some devices. The fields above win over an Extra of the
	// same name.
	Extra map[string]any `json:"-"`

	// TTL is how long the push service keeps the message for an offline
	// device. Zero uses the Sender's default.
	TTL time.Duration `json:"-"`
	// Urgency is empty for the Sender's default.
	Urgency Urgency `json:"-"`
	// Topic lets the push service replace an undelivered message with the
	// same topic (at most 32 base64url characters).
	Topic string `json:"-"`
}

// Payload is the JSON the service worker receives.
func (m Message) Payload() ([]byte, error) {
	if strings.TrimSpace(m.Title) == "" {
		return nil, apperr.New(CodeInvalidMessage, "webpush: message has no title")
	}
	b, err := m.encode()
	if err != nil {
		return nil, apperr.Wrap(CodeInvalidMessage, err)
	}
	if len(b) > MaxPayloadBytes {
		return nil, apperr.New(CodePayloadTooLarge,
			fmt.Sprintf("webpush: payload is %d bytes, the limit is %d", len(b), MaxPayloadBytes)).
			With("size", len(b)).With("limit", MaxPayloadBytes)
	}
	return b, nil
}

func (m Message) encode() ([]byte, error) {
	b, err := json.Marshal(m)
	if err != nil || len(m.Extra) == 0 {
		return b, err
	}
	fields := make(map[string]json.RawMessage, len(m.Extra))
	if err := json.Unmarshal(b, &fields); err != nil {
		return nil, err
	}
	for name, value := range m.Extra {
		if _, taken := fields[name]; taken {
			continue
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		fields[name] = raw
	}
	return json.Marshal(fields)
}
