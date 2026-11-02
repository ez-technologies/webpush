package webpush

import (
	"context"
	"strings"
	"time"

	"github.com/ez-technologies/apperr"
)

// Subscription is one browser's (or installed app's) address for pushes, as
// the browser's PushManager hands it out, tied to one of your users.
type Subscription struct {
	// UserID is your application's identifier of the user.
	UserID string `json:"userId"`
	// Endpoint is the push service URL. It is unique per subscription and is
	// used as its identity.
	Endpoint string `json:"endpoint"`
	// P256dh and Auth are the browser's encryption keys (keys.p256dh and
	// keys.auth in PushSubscription.toJSON()).
	P256dh string `json:"p256dh"`
	Auth   string `json:"auth"`
	// UserAgent and Lang are optional, for your own use (for example to word
	// a message in the device's language).
	UserAgent string    `json:"userAgent,omitempty"`
	Lang      string    `json:"lang,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// Validate reports a subscription that cannot be delivered to.
func (s Subscription) Validate() error {
	switch {
	case strings.TrimSpace(s.UserID) == "":
		return apperr.New(CodeInvalidSubscription, "webpush: subscription has no user")
	case !strings.HasPrefix(s.Endpoint, "https://"):
		return apperr.New(CodeInvalidSubscription, "webpush: subscription endpoint must be an https URL")
	case s.P256dh == "" || s.Auth == "":
		return apperr.New(CodeInvalidSubscription, "webpush: subscription keys p256dh and auth are required")
	}
	return nil
}

// Store keeps subscriptions by user.
//
// Save adds a subscription or replaces the one with the same endpoint (a
// browser that subscribes again, possibly for another user after a new
// login). Delete removes the user's subscription with that endpoint and is not
// an error when there is none. ListByUser returns every subscription of a
// user, oldest first.
type Store interface {
	Save(ctx context.Context, sub Subscription) error
	Delete(ctx context.Context, userID, endpoint string) error
	ListByUser(ctx context.Context, userID string) ([]Subscription, error)
}
