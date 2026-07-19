package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"ai-stock-service/internal/models"
)

// MarketDataRepo provides read-only queries over candles_daily that are
// specifically tuned for the Market Regime Engine's input requirements.
//
// All writes still go through CandlesDailyRepo. This repo exists solely to
// express the Regime Engine's read contract in one place and to keep the
// general-purpose CandlesDailyRepo free of regime-specific concerns.
type MarketDataRepo struct {
	// db is stored as the dbPool interface (defined in market_inputs_repo.go)
	// so that tests can inject a pgxmock without a live database connection.
	db dbPool
}

// NewMarketDataRepo creates a new MarketDataRepo.
// Compile-time assertion ensures *pgxpool.Pool always satisfies dbPool.
var _ dbPool = (*pgxpool.Pool)(nil)

// NewMarketDataRepo creates a new MarketDataRepo backed by a live pool.
func NewMarketDataRepo(db *pgxpool.Pool) *MarketDataRepo {
	return &MarketDataRepo{db: db}
}

// excludedTickers is the combined, deduplicated set of benchmark indices and
// sector ETFs that must be filtered out when fetching individual stock history.
// It is built once at startup from the canonical lists in models.
var excludedTickers = func() []string {
	out := make([]string, 0, len(models.Benchmarks)+len(models.SectorETFs))
	out = append(out, models.Benchmarks...)
	out = append(out, models.SectorETFs...)
	return out
}()

// GetIndexHistory returns the last `days` trading-day candles for a single
// ticker (benchmark index or sector ETF), sorted ascending by date
// (oldest → newest).
//
// Callers should request at least 250 days to ensure both SMA-50 and SMA-200
// can be computed from the returned slice.
//
// Returns an error if days <= 0 or if the ticker has no rows in candles_daily.
func (r *MarketDataRepo) GetIndexHistory(
	ctx context.Context,
	ticker string,
	days int,
) ([]models.CandleDaily, error) {
	if days <= 0 {
		return nil, fmt.Errorf("GetIndexHistory %s: days must be > 0, got %d", ticker, days)
	}

	// Fetch the most recent `days` rows, then reverse so the slice is
	// chronological (oldest first), which is the convention expected by all
	// indicator functions.
	rows, err := r.db.Query(ctx, `
		SELECT id, ticker, date,
		       open, high, low, close, adjusted_close,
		       volume, provider, created_at
		FROM   candles_daily
		WHERE  ticker = $1
		ORDER  BY date DESC
		LIMIT  $2
	`, ticker, days)
	if err != nil {
		return nil, fmt.Errorf("GetIndexHistory %s: %w", ticker, err)
	}
	defer rows.Close()

	out, err := scanCandles(rows)
	if err != nil {
		return nil, fmt.Errorf("GetIndexHistory %s: %w", ticker, err)
	}

	reverseCandles(out)
	return out, nil
}

// GetAllStocksHistory returns the last `days` trading-day candles for every
// individual equity in candles_daily, excluding benchmark indices (SPY, QQQ,
// IWM) and sector ETFs (XLK, XLF, …).
//
// The returned map is keyed by ticker symbol; each slice is sorted ascending
// by date (oldest → newest).
//
// The date window is anchored to SPY's own trading-day history so that `days`
// always means N actual market sessions — not calendar days — regardless of
// weekends and public holidays.
//
// Callers should request at least 250 days to cover the SMA-200 look-back
// for all breadth calculations.
func (r *MarketDataRepo) GetAllStocksHistory(
	ctx context.Context,
	days int,
) (map[string][]models.CandleDaily, error) {
	if days <= 0 {
		return nil, fmt.Errorf("GetAllStocksHistory: days must be > 0, got %d", days)
	}

	// ── Step 1: Resolve cutoff date ───────────────────────────────────────────
	// OFFSET (days-1) on SPY gives us the date of the Nth-most-recent trading
	// session. This is the oldest date we include in the result set.
	// SPY is used as the anchor because it is the most consistently populated
	// benchmark in the database.
	var cutoff time.Time
	err := r.db.QueryRow(ctx, `
		SELECT date
		FROM   candles_daily
		WHERE  ticker = 'SPY'
		ORDER  BY date DESC
		LIMIT  1 OFFSET $1
	`, days-1).Scan(&cutoff)
	if err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf(
				"GetAllStocksHistory: SPY has fewer than %d candles in the database — run backfill first",
				days,
			)
		}
		return nil, fmt.Errorf("GetAllStocksHistory cutoff: %w", err)
	}

	// ── Step 2: Fetch all stock candles from the cutoff date onward ───────────
	// Exclude benchmark indices and sector ETFs so only individual equities
	// are returned. Results are pre-sorted by ticker then date so we can
	// stream-append directly into the output map without any post-processing.
	rows, err := r.db.Query(ctx, `
		SELECT id, ticker, date,
		       open, high, low, close, adjusted_close,
		       volume, provider, created_at
		FROM   candles_daily
		WHERE  ticker != ALL($1::text[])
		  AND  date   >= $2
		ORDER  BY ticker ASC, date ASC
	`, excludedTickers, cutoff)
	if err != nil {
		return nil, fmt.Errorf("GetAllStocksHistory query: %w", err)
	}
	defer rows.Close()

	out := make(map[string][]models.CandleDaily)
	for rows.Next() {
		var c models.CandleDaily
		if err := rows.Scan(
			&c.ID, &c.Ticker, &c.Date,
			&c.Open, &c.High, &c.Low, &c.Close, &c.AdjustedClose,
			&c.Volume, &c.Provider, &c.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("GetAllStocksHistory scan: %w", err)
		}
		c.Ticker = strings.Clone(c.Ticker)
		c.Provider = strings.Clone(c.Provider)
		out[c.Ticker] = append(out[c.Ticker], c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("GetAllStocksHistory rows: %w", err)
	}

	return out, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// scanCandles reads every row from rows into a CandleDaily slice.
// The caller is responsible for closing rows and for reversing the slice if
// needed.
func scanCandles(rows pgx.Rows) ([]models.CandleDaily, error) {
	var out []models.CandleDaily
	for rows.Next() {
		var c models.CandleDaily
		if err := rows.Scan(
			&c.ID, &c.Ticker, &c.Date,
			&c.Open, &c.High, &c.Low, &c.Close, &c.AdjustedClose,
			&c.Volume, &c.Provider, &c.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan candle: %w", err)
		}
		c.Ticker = strings.Clone(c.Ticker)
		c.Provider = strings.Clone(c.Provider)
		out = append(out, c)
	}
	return out, rows.Err()
}

// reverseCandles reverses a CandleDaily slice in-place.
// Used to convert DESC query results to the ascending-date convention expected
// by all indicator functions.
func reverseCandles(s []models.CandleDaily) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

// GetLatestCandleDate returns the most recent candle date stored for ticker.
//
// The regime job calls this before computing inputs to verify that the candle
// ingest step actually fetched today's data.  If the returned date is before
// the target computation date, the job should abort rather than silently
// compute signals from stale data.
//
// Returns pgx.ErrNoRows (wrapped) when ticker has no candles in the database.
func (r *MarketDataRepo) GetLatestCandleDate(ctx context.Context, ticker string) (time.Time, error) {
	var d time.Time
	err := r.db.QueryRow(ctx, `
		SELECT MAX(date) FROM candles_daily WHERE ticker = $1
	`, ticker).Scan(&d)
	if err != nil {
		return time.Time{}, fmt.Errorf("GetLatestCandleDate %s: %w", ticker, err)
	}
	return d, nil
}
