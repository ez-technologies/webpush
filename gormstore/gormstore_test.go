package gormstore

import (
	"context"
	"os"
	"testing"

	"github.com/ez-technologies/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/ez-technologies/webpush"
)

// databases runs each test on SQLite in memory, and on PostgreSQL when
// WEBPUSH_TEST_POSTGRES_URL names one (for example
// postgres://postgres:test@127.0.0.1:55432/webpush?sslmode=disable).
func databases(t *testing.T) map[string]*gorm.DB {
	t.Helper()
	cfg := func() *gorm.Config { return &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)} }
	out := map[string]*gorm.DB{}
	lite, err := gorm.Open(sqlite.Open("file::memory:"), cfg())
	require.NoError(t, err)
	raw, err := lite.DB()
	require.NoError(t, err)
	raw.SetMaxOpenConns(1) // one connection, one in-memory database
	t.Cleanup(func() { _ = raw.Close() })
	out["sqlite"] = lite
	if url := os.Getenv("WEBPUSH_TEST_POSTGRES_URL"); url != "" {
		pg, err := gorm.Open(postgres.Open(url), cfg())
		require.NoError(t, err)
		out["postgres"] = pg
	}
	return out
}

func sub(user, endpoint string) webpush.Subscription {
	return webpush.Subscription{UserID: user, Endpoint: "https://push.example.com/" + endpoint, P256dh: "p-" + endpoint, Auth: "a-" + endpoint, Lang: "en"}
}

func TestStore(t *testing.T) {
	for name, db := range databases(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			s := New(db, Options{Table: "test_push_subs"})
			require.NoError(t, db.Exec("DROP TABLE IF EXISTS test_push_subs").Error)
			require.NoError(t, s.Migrate(ctx))
			require.NoError(t, s.Migrate(ctx), "migrating twice is fine")

			require.NoError(t, s.Save(ctx, sub("u1", "a")))
			require.NoError(t, s.Save(ctx, sub("u1", "b")))
			require.NoError(t, s.Save(ctx, sub("u2", "c")))

			list, err := s.ListByUser(ctx, "u1")
			require.NoError(t, err)
			require.Len(t, list, 2)
			assert.Equal(t, "https://push.example.com/a", list[0].Endpoint)
			assert.Equal(t, "p-a", list[0].P256dh)
			assert.Equal(t, "en", list[0].Lang)
			assert.False(t, list[0].CreatedAt.IsZero())

			// The same browser subscribing for another user moves the row.
			moved := sub("u2", "a")
			moved.Auth = "new"
			require.NoError(t, s.Save(ctx, moved))
			list, _ = s.ListByUser(ctx, "u1")
			assert.Len(t, list, 1)
			list, _ = s.ListByUser(ctx, "u2")
			require.Len(t, list, 2)
			var count int64
			db.Table("test_push_subs").Count(&count)
			assert.EqualValues(t, 3, count)

			// Delete only removes the user's own row.
			require.NoError(t, s.Delete(ctx, "u1", "https://push.example.com/a"))
			list, _ = s.ListByUser(ctx, "u2")
			assert.Len(t, list, 2)
			require.NoError(t, s.Delete(ctx, "u2", "https://push.example.com/a"))
			list, _ = s.ListByUser(ctx, "u2")
			require.Len(t, list, 1)
			assert.Equal(t, "https://push.example.com/c", list[0].Endpoint)

			err = s.Save(ctx, webpush.Subscription{UserID: "u1"})
			assert.Equal(t, webpush.CodeInvalidSubscription, apperr.CodeOf(err))
		})
	}
}

func TestStore_DefaultTableAndErrorCode(t *testing.T) {
	db := databases(t)["sqlite"]
	s := New(db)
	assert.Equal(t, DefaultTable, s.table)

	_, err := s.ListByUser(context.Background(), "u1") // not migrated
	assert.Equal(t, webpush.CodeStore, apperr.CodeOf(err))
}
