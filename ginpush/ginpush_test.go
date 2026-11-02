package ginpush

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ez-technologies/webpush"
)

func router(store webpush.Store) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(store, "PUBLIC-KEY", func(c *gin.Context) (string, bool) {
		u := c.GetHeader("X-User")
		return u, u != ""
	})
	h.Register(r.Group("/api/push"))
	return r
}

func do(r http.Handler, method, path, user, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "test-browser")
	if user != "" {
		req.Header.Set("X-User", user)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func errorOf(t *testing.T, w *httptest.ResponseRecorder) (string, string) {
	t.Helper()
	var body struct{ Error, Code string }
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return body.Error, body.Code
}

const subJSON = `{"endpoint":"https://push.example.com/abc","expirationTime":null,"keys":{"p256dh":"P","auth":"A"},"lang":"he"}`

func TestPublicKey(t *testing.T) {
	w := do(router(webpush.NewMemoryStore()), http.MethodGet, "/api/push/vapid-public-key", "", "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"publicKey":"PUBLIC-KEY"}`, w.Body.String())
}

func TestSubscribeAndUnsubscribe(t *testing.T) {
	store := webpush.NewMemoryStore()
	r := router(store)
	ctx := context.Background()

	w := do(r, http.MethodPost, "/api/push/subscriptions", "u1", subJSON)
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	list, _ := store.ListByUser(ctx, "u1")
	require.Len(t, list, 1)
	assert.Equal(t, webpush.Subscription{
		UserID: "u1", Endpoint: "https://push.example.com/abc", P256dh: "P", Auth: "A", Lang: "he",
		UserAgent: "test-browser", CreatedAt: list[0].CreatedAt,
	}, list[0])

	w = do(r, http.MethodDelete, "/api/push/subscriptions", "u2", `{"endpoint":"https://push.example.com/abc"}`)
	assert.Equal(t, http.StatusNoContent, w.Code)
	list, _ = store.ListByUser(ctx, "u1")
	assert.Len(t, list, 1, "another user cannot remove it")

	w = do(r, http.MethodDelete, "/api/push/subscriptions", "u1", `{"endpoint":"https://push.example.com/abc"}`)
	assert.Equal(t, http.StatusNoContent, w.Code)
	list, _ = store.ListByUser(ctx, "u1")
	assert.Empty(t, list)
}

func TestErrors(t *testing.T) {
	r := router(webpush.NewMemoryStore())
	cases := []struct {
		name, method, user, body string
		status                   int
		code                     string
	}{
		{"subscribe signed out", http.MethodPost, "", subJSON, 401, "webpush_unauthenticated"},
		{"unsubscribe signed out", http.MethodDelete, "", `{"endpoint":"x"}`, 401, "webpush_unauthenticated"},
		{"not json", http.MethodPost, "u1", "nope", 400, "webpush_invalid_subscription"},
		{"no keys", http.MethodPost, "u1", `{"endpoint":"https://push.example.com/x"}`, 400, "webpush_invalid_subscription"},
		{"http endpoint", http.MethodPost, "u1", `{"endpoint":"http://x","keys":{"p256dh":"P","auth":"A"}}`, 400, "webpush_invalid_subscription"},
		{"unsubscribe no endpoint", http.MethodDelete, "u1", `{}`, 400, "webpush_invalid_subscription"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := do(r, tc.method, "/api/push/subscriptions", tc.user, tc.body)
			assert.Equal(t, tc.status, w.Code)
			_, code := errorOf(t, w)
			assert.Equal(t, tc.code, code)
		})
	}
}

type brokenStore struct{ *webpush.MemoryStore }

func (*brokenStore) Save(context.Context, webpush.Subscription) error {
	return errors.New("disk on fire")
}

func TestStoreFailureHidesTheCause(t *testing.T) {
	w := do(router(&brokenStore{webpush.NewMemoryStore()}), http.MethodPost, "/api/push/subscriptions", "u1", subJSON)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	msg, code := errorOf(t, w)
	assert.Equal(t, "webpush_store_failed", code)
	assert.NotContains(t, msg, "fire")
}
