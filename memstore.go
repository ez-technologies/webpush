package webpush

import (
	"context"
	"sort"
	"sync"
	"time"
)

// MemoryStore is a Store held in memory, for tests, demos and single-process
// apps that can afford to lose subscriptions on restart.
type MemoryStore struct {
	mu   sync.Mutex
	subs map[string]Subscription // by endpoint
	now  func() time.Time
}

// NewMemoryStore makes an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{subs: map[string]Subscription{}, now: time.Now}
}

// Save implements Store.
func (m *MemoryStore) Save(_ context.Context, sub Subscription) error {
	if err := sub.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.subs[sub.Endpoint]; ok && sub.CreatedAt.IsZero() {
		sub.CreatedAt = old.CreatedAt
	}
	if sub.CreatedAt.IsZero() {
		sub.CreatedAt = m.now()
	}
	m.subs[sub.Endpoint] = sub
	return nil
}

// Delete implements Store.
func (m *MemoryStore) Delete(_ context.Context, userID, endpoint string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.subs[endpoint]; ok && old.UserID == userID {
		delete(m.subs, endpoint)
	}
	return nil
}

// ListByUser implements Store.
func (m *MemoryStore) ListByUser(_ context.Context, userID string) ([]Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Subscription
	for _, s := range m.subs {
		if s.UserID == userID {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].Endpoint < out[j].Endpoint
	})
	return out, nil
}
