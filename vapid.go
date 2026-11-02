package webpush

import (
	"crypto/ecdh"
	"encoding/base64"
	"strings"

	webpushgo "github.com/SherClockHolmes/webpush-go"
	"github.com/ez-technologies/apperr"
)

// VAPIDKeys is the key pair that identifies your server to the push services.
// Both halves are base64url without padding, the form browsers and push
// services expect. Public goes to the browser; Private stays on the server.
type VAPIDKeys struct {
	Public  string `json:"publicKey"`
	Private string `json:"privateKey"`
}

// GenerateVAPIDKeys makes a new P-256 key pair. Generate it once, keep it in
// your configuration, and reuse it: subscriptions are tied to the public key,
// so a new pair means every browser has to subscribe again.
func GenerateVAPIDKeys() (VAPIDKeys, error) {
	private, public, err := webpushgo.GenerateVAPIDKeys()
	if err != nil {
		return VAPIDKeys{}, apperr.Wrap(CodeInvalidKeys, err)
	}
	return VAPIDKeys{Public: public, Private: private}, nil
}

// Validate checks that both keys are present, are P-256 keys and belong
// together.
func (k VAPIDKeys) Validate() error {
	if k.Public == "" || k.Private == "" {
		return apperr.New(CodeNotConfigured, "webpush: VAPID public and private keys are required")
	}
	pub, err := decodeKey(k.Public)
	if err != nil || len(pub) != 65 {
		return apperr.New(CodeInvalidKeys, "webpush: VAPID public key is not a base64url P-256 point")
	}
	priv, err := decodeKey(k.Private)
	if err != nil {
		return apperr.New(CodeInvalidKeys, "webpush: VAPID private key is not base64url")
	}
	key, err := ecdh.P256().NewPrivateKey(priv)
	if err != nil {
		return apperr.New(CodeInvalidKeys, "webpush: VAPID private key is not a P-256 key")
	}
	if string(key.PublicKey().Bytes()) != string(pub) {
		return apperr.New(CodeInvalidKeys, "webpush: VAPID public key does not match the private key")
	}
	return nil
}

func decodeKey(s string) ([]byte, error) {
	s = strings.TrimRight(strings.TrimSpace(s), "=")
	return base64.RawURLEncoding.DecodeString(s)
}
