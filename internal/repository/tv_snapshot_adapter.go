package repository

import (
	"context"
	"fmt"
	"time"

	"ai-stock-service/internal/models"
)

// TickerSnapshotAdapter adapts TVSnapshotRepo to the tickerSnapshotSource
// interface used by NetReturnJob.
type TickerSnapshotAdapter struct {
	repo *TVSnapshotRepo
}

// NewTickerSnapshotAdapter creates a new adapter.
func NewTickerSnapshotAdapter(repo *TVSnapshotRepo) *TickerSnapshotAdapter {
	return &TickerSnapshotAdapter{repo: repo}
}

// GetSnapshot returns the price (close) and 20-day average dollar volume (ADV)
// for the given ticker on the given date. It looks up the most recent TV snapshot
// for the ticker on or before that date, and uses avg_volume_10d * close as an
// approximation of the 20-day ADV (since avg_volume_10d is the closest available
// volume average on the snapshot).
func (a *TickerSnapshotAdapter) GetSnapshot(ctx context.Context, ticker string, date time.Time) (*models.TickerSnapshot, error) {
	snapshots, err := a.repo.LatestByTicker(ctx, ticker)
	if err != nil {
		return nil, fmt.Errorf("TickerSnapshotAdapter.GetSnapshot: %w", err)
	}

	// Find the closest snapshot on or before the entry date.
	var best struct {
		close float64
		vol   float64
		date  time.Time
		found bool
	}

	for i := range snapshots {
		s := &snapshots[i]
		if s.SnapshotDate.After(date) {
			continue
		}
		if !best.found || s.SnapshotDate.After(best.date) {
			best.found = true
			best.date = s.SnapshotDate
			if s.Close != nil {
				best.close = *s.Close
			}
			if s.AvgVolume10d != nil {
				best.vol = float64(*s.AvgVolume10d)
			}
		}
	}

	if !best.found || best.close <= 0 {
		return nil, fmt.Errorf("TickerSnapshotAdapter.GetSnapshot: no snapshot found for %s on/before %s",
			ticker, date.Format("2006-01-02"))
	}

	// ADV approximation: avg_volume_10d * close.
	adv := best.vol * best.close

	return &models.TickerSnapshot{
		Price: best.close,
		ADV:   adv,
	}, nil
}
