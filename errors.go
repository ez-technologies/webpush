package webpush

import "github.com/ez-technologies/apperr"

// Error codes carried by the errors of this module. Read one with
// apperr.CodeOf(err).
const (
	// CodeNotConfigured: the sender has no VAPID key pair.
	CodeNotConfigured apperr.Code = "webpush_not_configured"
	// CodeInvalidKeys: a VAPID key is not a valid P-256 key in base64url.
	CodeInvalidKeys apperr.Code = "webpush_invalid_keys"
	// CodeInvalidSubscription: a subscription lacks its endpoint, its keys or its user.
	CodeInvalidSubscription apperr.Code = "webpush_invalid_subscription"
	// CodeInvalidMessage: a message has no title or cannot be encoded.
	CodeInvalidMessage apperr.Code = "webpush_invalid_message"
	// CodePayloadTooLarge: the encoded message is larger than push services accept.
	CodePayloadTooLarge apperr.Code = "webpush_payload_too_large"
	// CodeDeliveryFailed: no subscription of the user could be reached.
	CodeDeliveryFailed apperr.Code = "webpush_delivery_failed"
	// CodeStore: the subscription store failed.
	CodeStore apperr.Code = "webpush_store_failed"
	// CodeUnauthenticated: a handler could not tell which user is calling.
	CodeUnauthenticated apperr.Code = "webpush_unauthenticated"
)

func init() {
	apperr.Register(CodeNotConfigured, CodeInvalidKeys, CodeInvalidSubscription, CodeInvalidMessage, CodePayloadTooLarge,
		CodeDeliveryFailed, CodeStore, CodeUnauthenticated)
}
