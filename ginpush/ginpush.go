// Package ginpush gives a Gin router the three endpoints the browser side of
// Web Push needs: the public VAPID key, save a subscription, delete it.
//
//	h := ginpush.New(store, keys.Public, func(c *gin.Context) (string, bool) {
//		return c.GetString("userID"), c.GetString("userID") != ""
//	})
//	h.Register(router.Group("/api/push", requireLogin))
//
// gives
//
//	GET    /api/push/vapid-public-key   -> {"publicKey": "..."}
//	POST   /api/push/subscriptions      <- PushSubscription.toJSON() (+ "lang")
//	DELETE /api/push/subscriptions      <- {"endpoint": "..."}
//
// Errors are answered as {"error": "...", "code": "..."} with the codes of
// package webpush.
package ginpush

import (
	"errors"
	"net/http"

	"github.com/ez-technologies/apperr"
	"github.com/gin-gonic/gin"

	"github.com/ez-technologies/webpush"
)

// UserFunc tells which user is calling. It returns false for a caller who is
// not signed in; the handlers then answer 401.
type UserFunc func(c *gin.Context) (userID string, ok bool)

// Handlers serves the endpoints. Create it with New.
type Handlers struct {
	store     webpush.Store
	publicKey string
	user      UserFunc
}

// New builds the handlers.
func New(store webpush.Store, publicKey string, user UserFunc) *Handlers {
	return &Handlers{store: store, publicKey: publicKey, user: user}
}

// Register mounts the endpoints on r (a router or a group).
func (h *Handlers) Register(r gin.IRoutes) {
	r.GET("/vapid-public-key", h.PublicKey)
	r.POST("/subscriptions", h.Subscribe)
	r.DELETE("/subscriptions", h.Unsubscribe)
}

// SubscribeRequest is the body of POST /subscriptions: the browser's
// PushSubscription.toJSON(), with an optional language.
type SubscribeRequest struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
	Lang string `json:"lang"`
}

// UnsubscribeRequest is the body of DELETE /subscriptions.
type UnsubscribeRequest struct {
	Endpoint string `json:"endpoint"`
}

// PublicKey answers {"publicKey": "..."}. It needs no signed-in user.
func (h *Handlers) PublicKey(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"publicKey": h.publicKey})
}

// Subscribe saves the caller's subscription and answers 204.
func (h *Handlers) Subscribe(c *gin.Context) {
	userID, ok := h.user(c)
	if !ok {
		fail(c, http.StatusUnauthorized, apperr.New(webpush.CodeUnauthenticated, "not signed in"))
		return
	}
	var req SubscribeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, apperr.New(webpush.CodeInvalidSubscription, "body is not a push subscription"))
		return
	}
	sub := webpush.Subscription{
		UserID: userID, Endpoint: req.Endpoint, P256dh: req.Keys.P256dh, Auth: req.Keys.Auth,
		Lang: req.Lang, UserAgent: truncate(c.Request.UserAgent(), 512),
	}
	if err := h.store.Save(c.Request.Context(), sub); err != nil {
		fail(c, statusOf(err), err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Unsubscribe deletes the caller's subscription with that endpoint and
// answers 204, also when there was none.
func (h *Handlers) Unsubscribe(c *gin.Context) {
	userID, ok := h.user(c)
	if !ok {
		fail(c, http.StatusUnauthorized, apperr.New(webpush.CodeUnauthenticated, "not signed in"))
		return
	}
	var req UnsubscribeRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Endpoint == "" {
		fail(c, http.StatusBadRequest, apperr.New(webpush.CodeInvalidSubscription, "endpoint is required"))
		return
	}
	if err := h.store.Delete(c.Request.Context(), userID, req.Endpoint); err != nil {
		fail(c, statusOf(err), err)
		return
	}
	c.Status(http.StatusNoContent)
}

func statusOf(err error) int {
	if apperr.CodeOf(err) == webpush.CodeInvalidSubscription {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

func fail(c *gin.Context, status int, err error) {
	msg := err.Error()
	code := apperr.CodeOf(err)
	if status >= 500 {
		_ = c.Error(err)
		msg = "subscription store failed"
		var coded *apperr.Error
		if !errors.As(err, &coded) {
			code = webpush.CodeStore
		}
	}
	c.AbortWithStatusJSON(status, gin.H{"error": msg, "code": code})
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
