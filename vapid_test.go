package webpush

import (
	"testing"

	"github.com/ez-technologies/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateVAPIDKeys_MakesAValidPair(t *testing.T) {
	k, err := GenerateVAPIDKeys()
	require.NoError(t, err)

	assert.Len(t, k.Public, 87) // 65 bytes in base64url without padding
	assert.Len(t, k.Private, 43)
	assert.NoError(t, k.Validate())

	other, err := GenerateVAPIDKeys()
	require.NoError(t, err)
	assert.NotEqual(t, k.Private, other.Private)
}

func TestVAPIDKeys_Validate(t *testing.T) {
	a, _ := GenerateVAPIDKeys()
	b, _ := GenerateVAPIDKeys()

	cases := map[string]struct {
		keys VAPIDKeys
		code apperr.Code
	}{
		"empty":          {VAPIDKeys{}, CodeNotConfigured},
		"no private key": {VAPIDKeys{Public: a.Public}, CodeNotConfigured},
		"garbage public": {VAPIDKeys{Public: "not a key!", Private: a.Private}, CodeInvalidKeys},
		"short public":   {VAPIDKeys{Public: "AAAA", Private: a.Private}, CodeInvalidKeys},
		"garbage priv":   {VAPIDKeys{Public: a.Public, Private: "%%%"}, CodeInvalidKeys},
		"mismatched":     {VAPIDKeys{Public: a.Public, Private: b.Private}, CodeInvalidKeys},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.code, apperr.CodeOf(tc.keys.Validate()))
		})
	}

	padded := VAPIDKeys{Public: a.Public + "=", Private: a.Private + "\n"}
	assert.NoError(t, padded.Validate(), "padding and surrounding space are tolerated")
}

func TestErrorCodes_AreRegistered(t *testing.T) {
	assert.Subset(t, apperr.Codes(), []string{
		"webpush_not_configured", "webpush_invalid_keys", "webpush_invalid_subscription", "webpush_invalid_message",
		"webpush_payload_too_large", "webpush_delivery_failed", "webpush_store_failed", "webpush_unauthenticated",
	})
}
