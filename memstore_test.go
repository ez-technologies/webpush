package webpush

import (
	"context"
	"testing"
	"time"

	"github.com/ez-technologies/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sub(user, endpoint string) Subscription {
	return Subscription{UserID: user, Endpoint: "https://push.example.com/" + endpoint, P256dh: "p256dh-" + endpoint, Auth: "auth-" + endpoint}
}

func TestSubscription_Validate(t *testing.T) {
	assert.NoError(t, sub("u1", "a").Validate())

	noUser := sub("", "a")
	httpEndpoint := sub("u1", "a")
	httpEndpoint.Endpoint = "http://push.example.com/a"
	noKeys := sub("u1", "a")
	noKeys.Auth = ""
	for _, s := range []Subscription{noUser, httpEndpoint, noKeys} {
		assert.Equal(t, CodeInvalidSubscription, apperr.CodeOf(s.Validate()))
	}
}

func TestMemoryStore_SaveListDelete(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m.now = func() time.Time { clock = clock.Add(time.Second); return clock }

	require.NoError(t, m.Save(ctx, sub("u1", "b")))
	require.NoError(t, m.Save(ctx, sub("u1", "a")))
	require.NoError(t, m.Save(ctx, sub("u2", "c")))

	list, err := m.ListByUser(ctx, "u1")
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, "https://push.example.com/b", list[0].Endpoint, "oldest first")
	assert.False(t, list[0].CreatedAt.IsZero())

	// Deleting someone else's endpoint does nothing.
	require.NoError(t, m.Delete(ctx, "u2", "https://push.example.com/a"))
	list, _ = m.ListByUser(ctx, "u1")
	assert.Len(t, list, 2)

	require.NoError(t, m.Delete(ctx, "u1", "https://push.example.com/a"))
	require.NoError(t, m.Delete(ctx, "u1", "https://push.example.com/missing"))
	list, _ = m.ListByUser(ctx, "u1")
	assert.Len(t, list, 1)

	none, err := m.ListByUser(ctx, "nobody")
	require.NoError(t, err)
	assert.Empty(t, none)
}

func TestMemoryStore_SaveSameEndpointMovesItAndKeepsCreatedAt(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	require.NoError(t, m.Save(ctx, sub("u1", "a")))
	first, _ := m.ListByUser(ctx, "u1")

	moved := sub("u2", "a")
	moved.Auth = "new-auth"
	require.NoError(t, m.Save(ctx, moved))

	gone, _ := m.ListByUser(ctx, "u1")
	assert.Empty(t, gone)
	now, _ := m.ListByUser(ctx, "u2")
	require.Len(t, now, 1)
	assert.Equal(t, "new-auth", now[0].Auth)
	assert.Equal(t, first[0].CreatedAt, now[0].CreatedAt)
}

func TestMemoryStore_RejectsInvalid(t *testing.T) {
	err := NewMemoryStore().Save(context.Background(), Subscription{UserID: "u1"})
	assert.Equal(t, CodeInvalidSubscription, apperr.CodeOf(err))
}
