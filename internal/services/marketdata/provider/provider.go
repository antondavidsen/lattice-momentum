// Package provider defines the Provider interface and shared utilities for
// market data sources. Each concrete implementation (Polygon, TwelveData, …)
// satisfies this interface, allowing the rest of the system to remain
// provider-agnostic.
package provider

import (
	"context"
	"time"

	"ai-stock-service/internal/models"
)

// Provider is the abstraction for a historical market data source.
// Implementations must be safe for concurrent use.
type Provider interface {
	// Name returns the canonical provider identifier (e.g. "polygon", "twelvedata").
	// The value is stored in the candles_daily.provider column.
	Name() string

	// FetchDailyCandles retrieves adjusted daily OHLCV bars for the given ticker
	// for all trading days in [from, to] (both inclusive, UTC midnight).
	// The returned slice is ordered ascending by date.
	// An empty slice (not an error) is returned when no data exists for the range.
	FetchDailyCandles(ctx context.Context, ticker string, from, to time.Time) ([]models.CandleDaily, error)
}
