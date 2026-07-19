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

// TVSnapshotRepo handles persistence for tradingview_snapshot_daily.
type TVSnapshotRepo struct {
	db *pgxpool.Pool
}

// NewTVSnapshotRepo creates a new TVSnapshotRepo backed by a live pool.
func NewTVSnapshotRepo(db *pgxpool.Pool) *TVSnapshotRepo {
	return &TVSnapshotRepo{db: db}
}

// InsertBatch upserts a slice of snapshots (all from the same screener_source)
// in a single pgx pipeline batch.
//
// ON CONFLICT (ticker, snapshot_date, screener_source): all mutable columns are
// overwritten so that re-running a collection replaces stale values.
func (r *TVSnapshotRepo) InsertBatch(ctx context.Context, snaps []models.TradingViewSnapshotDaily) error {
	if len(snaps) == 0 {
		return nil
	}

	const upsertSQL = `
		INSERT INTO tradingview_snapshot_daily
			(ticker, snapshot_date, screener_source,
			 open, high, low, close, volume, relative_volume, price_x_volume_10d, market_cap, avg_volume_10d,
			 rsi_14, perf_3m, perf_6m, distance_52w_high, price_52w_high, price_52w_low,
			 gap_pct, change_pct,
			 sma_20, sma_50, sma_150, sma_200, atr_14,
			 float_shares,
			 eps_ttm, eps_growth_yoy, revenue_ttm, revenue_growth_yoy,
			 roe, gross_margin, net_margin, operating_margin,
			 earnings_date, sector, exchange, raw_json)
		VALUES (
			$1,$2,$3,
			$4,$5,$6,$7,$8,$9,$10,$11,$12,
			$13,$14,$15,$16,$17,$18,
			$19,$20,
			$21,$22,$23,$24,$25,
			$26,
			$27,$28,$29,$30,
			$31,$32,$33,$34,
			$35,$36,$37,$38::jsonb
		)
		ON CONFLICT (ticker, snapshot_date, screener_source) DO UPDATE SET
			open               = EXCLUDED.open,
			high               = EXCLUDED.high,
			low                = EXCLUDED.low,
			close              = EXCLUDED.close,
			volume             = EXCLUDED.volume,
			relative_volume    = EXCLUDED.relative_volume,
			price_x_volume_10d = EXCLUDED.price_x_volume_10d,
			market_cap         = EXCLUDED.market_cap,
			avg_volume_10d     = EXCLUDED.avg_volume_10d,
			rsi_14             = EXCLUDED.rsi_14,
			perf_3m            = EXCLUDED.perf_3m,
			perf_6m            = EXCLUDED.perf_6m,
			distance_52w_high  = EXCLUDED.distance_52w_high,
			price_52w_high     = EXCLUDED.price_52w_high,
			price_52w_low      = EXCLUDED.price_52w_low,
			gap_pct            = EXCLUDED.gap_pct,
			change_pct         = EXCLUDED.change_pct,
			sma_20             = EXCLUDED.sma_20,
			sma_50             = EXCLUDED.sma_50,
			sma_150            = EXCLUDED.sma_150,
			sma_200            = EXCLUDED.sma_200,
			atr_14             = EXCLUDED.atr_14,
			float_shares       = EXCLUDED.float_shares,
			eps_ttm            = EXCLUDED.eps_ttm,
			eps_growth_yoy     = EXCLUDED.eps_growth_yoy,
			revenue_ttm        = EXCLUDED.revenue_ttm,
			revenue_growth_yoy = EXCLUDED.revenue_growth_yoy,
			roe                = EXCLUDED.roe,
			gross_margin       = EXCLUDED.gross_margin,
			net_margin         = EXCLUDED.net_margin,
			operating_margin   = EXCLUDED.operating_margin,
			earnings_date      = EXCLUDED.earnings_date,
			sector             = EXCLUDED.sector,
			exchange           = EXCLUDED.exchange,
			raw_json           = EXCLUDED.raw_json
	`

	batch := &pgx.Batch{}
	for i := range snaps {
		s := &snaps[i]
		batch.Queue(upsertSQL,
			s.Ticker, s.SnapshotDate, string(s.ScreenerSource),
			s.Open, s.High, s.Low, s.Close, s.Volume, s.RelativeVolume, s.PriceXVolume10d, s.MarketCap, s.AvgVolume10d,
			s.RSI14, s.Perf3M, s.Perf6M, s.Distance52wHigh, s.Price52wHigh, s.Price52wLow,
			s.GapPct, s.ChangePct,
			s.SMA20, s.SMA50, s.SMA150, s.SMA200, s.ATR14,
			s.FloatShares,
			s.EPSTTM, s.EPSGrowthYOY, s.RevenueTTM, s.RevenueGrowthYOY,
			s.ROE, s.GrossMargin, s.NetMargin, s.OperatingMargin,
			s.EarningsDate, s.Sector, s.Exchange, s.RawJSON,
		)
	}

	br := r.db.SendBatch(ctx, batch)
	defer func() { _ = br.Close() }()

	for i := range snaps {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("insert tv_snapshot[%d] %s/%s: %w",
				i, snaps[i].ScreenerSource, snaps[i].Ticker, err)
		}
	}
	return nil
}

// LatestByTicker returns the most recent snapshots for a ticker across all
// screener sources.
func (r *TVSnapshotRepo) LatestByTicker(ctx context.Context, ticker string) ([]models.TradingViewSnapshotDaily, error) {
	rows, err := r.db.Query(ctx, `
		SELECT DISTINCT ON (screener_source)
			id, ticker, snapshot_date, screener_source,
			open, high, low, close, volume, relative_volume, price_x_volume_10d, market_cap, avg_volume_10d,
			rsi_14, perf_3m, perf_6m, distance_52w_high, price_52w_high, price_52w_low,
			gap_pct, change_pct,
			sma_20, sma_50, sma_150, sma_200, atr_14,
			float_shares,
			eps_ttm, eps_growth_yoy, revenue_ttm, revenue_growth_yoy,
			roe, gross_margin, net_margin, operating_margin,
			earnings_date, sector, exchange, raw_json, created_at
		FROM tradingview_snapshot_daily
		WHERE ticker = $1
		ORDER BY screener_source, snapshot_date DESC
	`, ticker)
	if err != nil {
		return nil, fmt.Errorf("query latest tv snapshots for %s: %w", ticker, err)
	}
	defer rows.Close()

	return scanTVSnapshots(rows)
}

// ListByDate returns every snapshot row whose snapshot_date equals date,
// across all tickers and screener sources.
// Used by the fundamentals promoter (Step 2) to extract per-ticker
// fundamentals from today's screener run.
func (r *TVSnapshotRepo) ListByDate(ctx context.Context, date time.Time) ([]models.TradingViewSnapshotDaily, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, ticker, snapshot_date, screener_source,
			open, high, low, close, volume, relative_volume, price_x_volume_10d, market_cap, avg_volume_10d,
			rsi_14, perf_3m, perf_6m, distance_52w_high, price_52w_high, price_52w_low,
			gap_pct, change_pct,
			sma_20, sma_50, sma_150, sma_200, atr_14,
			float_shares,
			eps_ttm, eps_growth_yoy, revenue_ttm, revenue_growth_yoy,
			roe, gross_margin, net_margin, operating_margin,
			earnings_date, sector, exchange, raw_json, created_at
		FROM tradingview_snapshot_daily
		WHERE snapshot_date = $1
		ORDER BY ticker ASC, screener_source ASC
	`, date)
	if err != nil {
		return nil, fmt.Errorf("query tv snapshots for date %s: %w", date.Format("2006-01-02"), err)
	}
	defer rows.Close()

	return scanTVSnapshots(rows)
}

// TickersByDateGrouped returns tickers for a given date, grouped by screener source.
// The result maps screener_source → sorted list of tickers.
func (r *TVSnapshotRepo) TickersByDateGrouped(ctx context.Context, date time.Time) (map[string][]string, error) {
	rows, err := r.db.Query(ctx, `
		SELECT screener_source, ticker
		FROM tradingview_snapshot_daily
		WHERE snapshot_date = $1
		ORDER BY screener_source, ticker
	`, date)
	if err != nil {
		return nil, fmt.Errorf("query screener tickers for %s: %w", date.Format("2006-01-02"), err)
	}
	defer rows.Close()

	result := make(map[string][]string)
	for rows.Next() {
		var source, ticker string
		if err := rows.Scan(&source, &ticker); err != nil {
			return nil, fmt.Errorf("scan screener ticker: %w", err)
		}
		result[strings.Clone(source)] = append(result[strings.Clone(source)], strings.Clone(ticker))
	}
	return result, rows.Err()
}

// CountByDate returns the total number of snapshot rows for the given date
// across all tickers and screener sources.
// The nightly pipeline uses this to verify that the tv-collector has already
// populated data for today before proceeding to Step 2.
func (r *TVSnapshotRepo) CountByDate(ctx context.Context, date time.Time) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM tradingview_snapshot_daily WHERE snapshot_date = $1
	`, date).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count tv snapshots for date %s: %w", date.Format("2006-01-02"), err)
	}
	return n, nil
}

// scanTVSnapshots reads all rows from a pgx.Rows cursor into a slice.
//
// IMPORTANT: Every value is defensively copied to fully detach from pgx's
// internal connection buffers.  Without this, the GC can find dangling
// pointers after the connection returns to the pool — producing
// "found bad pointer in Go heap" fatals under concurrent load.
//
// The raw_json column is scanned as plain []byte (not json.RawMessage) to
// bypass pgx's scanPlanJSONToJSONUnmarshal which uses unsafe reflection and
// can segfault when the stored JSON has an unexpected shape.
func scanTVSnapshots(rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}) ([]models.TradingViewSnapshotDaily, error) {
	var out []models.TradingViewSnapshotDaily
	for rows.Next() {
		var s models.TradingViewSnapshotDaily
		var src string
		var rawJSON []byte
		if err := rows.Scan(
			&s.ID, &s.Ticker, &s.SnapshotDate, &src,
			&s.Open, &s.High, &s.Low, &s.Close, &s.Volume, &s.RelativeVolume, &s.PriceXVolume10d, &s.MarketCap, &s.AvgVolume10d,
			&s.RSI14, &s.Perf3M, &s.Perf6M, &s.Distance52wHigh, &s.Price52wHigh, &s.Price52wLow,
			&s.GapPct, &s.ChangePct,
			&s.SMA20, &s.SMA50, &s.SMA150, &s.SMA200, &s.ATR14,
			&s.FloatShares,
			&s.EPSTTM, &s.EPSGrowthYOY, &s.RevenueTTM, &s.RevenueGrowthYOY,
			&s.ROE, &s.GrossMargin, &s.NetMargin, &s.OperatingMargin,
			&s.EarningsDate, &s.Sector, &s.Exchange, &rawJSON, &s.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan tv snapshot: %w", err)
		}

		// ── Defensive copies — fully detach from pgx connection buffers ───
		s.Ticker = strings.Clone(s.Ticker)
		s.ScreenerSource = models.ScreenerSource(strings.Clone(src))
		if s.Sector != nil {
			c := strings.Clone(*s.Sector)
			s.Sector = &c
		}
		if s.Exchange != nil {
			c := strings.Clone(*s.Exchange)
			s.Exchange = &c
		}
		if len(rawJSON) > 0 {
			s.RawJSON = append([]byte(nil), rawJSON...)
		}

		out = append(out, s)
	}
	return out, rows.Err()
}
