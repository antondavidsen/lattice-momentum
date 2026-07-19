package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// ListType identifies which ranking list a row belongs to.
type ListType string

// Valid list types are "ep", "momentum", and "leaders".
const (
	ListTypeEP       ListType = "ep"
	ListTypeMomentum ListType = "momentum"
	ListTypeLeaders  ListType = "leaders"
)

// DailyRankList is one row in the daily_rank_lists table.
// It stores a single ranked ticker for a given date and list type.
type DailyRankList struct {
	ID        uuid.UUID       `db:"id"`
	Date      time.Time       `db:"date"`
	ListType  ListType        `db:"list_type"`
	Rank      int             `db:"rank"`
	Ticker    string          `db:"ticker"`
	Score     float64         `db:"score"`
	Reason    json.RawMessage `db:"reason"`
	CreatedAt time.Time       `db:"created_at"`
}
