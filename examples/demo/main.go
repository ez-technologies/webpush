// Command demo is a small web app that subscribes the browser to Web Push and
// sends it a test push and a throttled, merged push.
//
//	npm install && npm run build      # builds client/dist (the browser helpers)
//	go run ./examples/demo            # then open http://localhost:8080
//
// It makes a new VAPID key pair at every start unless VAPID_PUBLIC_KEY and
// VAPID_PRIVATE_KEY are set; with new keys, subscribe again after a restart.
// Subscriptions are kept in memory. Each browser gets its own demo user from a
// cookie.
package main

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ez-technologies/webpush"
	"github.com/ez-technologies/webpush/ginpush"
)

//go:embed index.html
var page []byte

func main() {
	addr := flag.String("addr", "localhost:8080", "listen address")
	dist := flag.String("client", "client/dist", "folder with the built browser helpers (npm run build)")
	window := flag.Duration("window", 15*time.Second, "throttle window for the merged push")
	flag.Parse()

	if _, err := os.Stat(filepath.Join(*dist, "push-sw.js")); err != nil {
		log.Fatalf("demo: %s/push-sw.js not found; run `npm install && npm run build` in the repository root first", *dist)
	}

	keys := webpush.VAPIDKeys{Public: os.Getenv("VAPID_PUBLIC_KEY"), Private: os.Getenv("VAPID_PRIVATE_KEY")}
	if keys.Public == "" {
		var err error
		if keys, err = webpush.GenerateVAPIDKeys(); err != nil {
			log.Fatal(err)
		}
		log.Print("demo: generated a new VAPID key pair for this run")
	}

	store := webpush.NewMemoryStore()
	sender, err := webpush.NewSender(webpush.Config{Keys: keys, Subject: "mailto:demo@example.com", TTL: time.Hour}, store,
		webpush.WithErrorHandler(func(err error) { log.Print("demo: push: ", err) }))
	if err != nil {
		log.Fatal(err)
	}
	throttler := webpush.NewThrottler(sender, webpush.ThrottleConfig{
		Window: *window,
		Merge: webpush.MergeWith(func(n int, _ string, newest webpush.Message) string {
			return fmt.Sprintf("%d new messages in %s", n, newest.Title)
		}),
		OnError: func(user, group string, err error) { log.Printf("demo: merged push to %s/%s: %v", user, group, err) },
	})

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), demoUser)

	api := r.Group("/api/push")
	ginpush.New(store, sender.PublicKey(), func(c *gin.Context) (string, bool) {
		u := c.GetString("user")
		return u, u != ""
	}).Register(api)

	r.POST("/api/demo/test", func(c *gin.Context) {
		res, err := sender.Send(c.Request.Context(), c.GetString("user"), webpush.Message{
			Title: "Test push",
			Body:  "Sent at " + time.Now().Format("15:04:05") + ". Tap to open the demo.",
			URL:   "/?from=notification",
			Tag:   "test",
		})
		answer(c, gin.H{"sent": res.Sent, "removed": res.Removed, "failed": len(res.Failed)}, err)
	})

	r.POST("/api/demo/burst", func(c *gin.Context) {
		var outcomes []string
		for i, text := range []string{"The new logo draft is ready for review.", "Two variants are in the shared folder.", "The blue one has the most votes.", "Review moved to 3pm."} {
			out, err := throttler.Notify(c.Request.Context(), c.GetString("user"), "design-team", webpush.Message{
				Title: "Design team",
				Body:  text,
				URL:   "/?from=notification&chat=design-team",
				Tag:   "chat-design-team",
				Data:  map[string]any{"index": i + 1},
			})
			if err != nil {
				answer(c, nil, err)
				return
			}
			outcomes = append(outcomes, out.String())
		}
		c.JSON(http.StatusOK, gin.H{"outcomes": outcomes, "windowSeconds": int(window.Seconds())})
	})

	r.GET("/api/demo/me", func(c *gin.Context) {
		subs, _ := store.ListByUser(c.Request.Context(), c.GetString("user"))
		c.JSON(http.StatusOK, gin.H{"user": c.GetString("user"), "subscriptions": len(subs)})
	})

	r.StaticFile("/push-sw.js", filepath.Join(*dist, "push-sw.js"))
	r.Static("/client", *dist)
	r.GET("/", func(c *gin.Context) { c.Data(http.StatusOK, "text/html; charset=utf-8", page) })

	srv := &http.Server{Addr: *addr, Handler: r, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("demo: open http://%s", *addr)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	throttler.Flush(ctx)
	_ = srv.Shutdown(ctx)
}

// demoUser gives every browser its own user id, in a cookie. A real app uses
// its login instead.
func demoUser(c *gin.Context) {
	id, err := c.Cookie("demo_user")
	if err != nil || id == "" {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		id = "demo-" + hex.EncodeToString(b)
		c.SetCookie("demo_user", id, 30*24*3600, "/", "", false, true)
	}
	c.Set("user", id)
	c.Next()
}

func answer(c *gin.Context, body gin.H, err error) {
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, body)
}
