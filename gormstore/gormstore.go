// Package gormstore keeps Web Push subscriptions in a SQL database through
// GORM. It is written for PostgreSQL and works with any GORM driver that
// supports ON CONFLICT upserts (SQLite included).
package gormstore

import (
	"context"
	"time"

	"github.com/ez-technologies/apperr"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/ez-technologies/webpush"
)

// DefaultTable is the table name used when Options.Table is empty.
const DefaultTable = "webpush_subscriptions"

// Record is the row of one subscription.
type Record struct {
	ID        uint   `gorm:"primaryKey"`
	UserID    string `gorm:"size:191;not null;index"`
	Endpoint  string `gorm:"type:text;not null;uniqueIndex"`
	P256dh    string `gorm:"size:255;not null"`
	Auth      string `gorm:"size:255;not null"`
	UserAgent string `gorm:"size:512"`
	Lang      string `gorm:"size:35"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Store implements webpush.Store on a *gorm.DB.
type Store struct {
	db    *gorm.DB
	table string
}

// Options configures a Store.
type Options struct {
	// Table is the table name. Default DefaultTable.
	Table string
}

// New builds a Store. Call Migrate once (or create the table with your own
// migrations; see Record for its columns).
func New(db *gorm.DB, opts ...Options) *Store {
	s := &Store{db: db, table: DefaultTable}
	if len(opts) > 0 && opts[0].Table != "" {
		s.table = opts[0].Table
	}
	return s
}

// Migrate creates or updates the table.
func (s *Store) Migrate(ctx context.Context) error {
	return s.wrap(s.tx(ctx).AutoMigrate(&Record{}))
}

func (s *Store) tx(ctx context.Context) *gorm.DB { return s.db.WithContext(ctx).Table(s.table) }

func (s *Store) wrap(err error) error {
	if err == nil {
		return nil
	}
	return apperr.Wrap(webpush.CodeStore, err)
}

// Save implements webpush.Store: insert, or update the row with the same
// endpoint (keeping its creation time).
func (s *Store) Save(ctx context.Context, sub webpush.Subscription) error {
	if err := sub.Validate(); err != nil {
		return err
	}
	rec := Record{
		UserID: sub.UserID, Endpoint: sub.Endpoint, P256dh: sub.P256dh, Auth: sub.Auth,
		UserAgent: sub.UserAgent, Lang: sub.Lang, CreatedAt: sub.CreatedAt,
	}
	return s.wrap(s.tx(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "endpoint"}},
		DoUpdates: clause.AssignmentColumns([]string{"user_id", "p256dh", "auth", "user_agent", "lang", "updated_at"}),
	}).Create(&rec).Error)
}

// Delete implements webpush.Store.
func (s *Store) Delete(ctx context.Context, userID, endpoint string) error {
	return s.wrap(s.tx(ctx).Where("user_id = ? AND endpoint = ?", userID, endpoint).Delete(&Record{}).Error)
}

// ListByUser implements webpush.Store.
func (s *Store) ListByUser(ctx context.Context, userID string) ([]webpush.Subscription, error) {
	var recs []Record
	if err := s.tx(ctx).Where("user_id = ?", userID).Order("created_at, id").Find(&recs).Error; err != nil {
		return nil, s.wrap(err)
	}
	out := make([]webpush.Subscription, len(recs))
	for i, r := range recs {
		out[i] = webpush.Subscription{
			UserID: r.UserID, Endpoint: r.Endpoint, P256dh: r.P256dh, Auth: r.Auth,
			UserAgent: r.UserAgent, Lang: r.Lang, CreatedAt: r.CreatedAt,
		}
	}
	return out, nil
}

var _ webpush.Store = (*Store)(nil)
