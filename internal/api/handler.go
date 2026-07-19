// Package api contains HTTP handlers for the Momentum AI REST API.
package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/google/uuid"

	"ai-stock-service/internal/models"
	"ai-stock-service/internal/repository"
)

const maxRequestBodyBytes = 10 << 20 // 10 MB — generous for 3×200 screener rows

// Handler holds injected dependencies for all API endpoints.
type Handler struct {
	snapshots   *repository.SnapshotRepo
	tickers     *repository.TickerRepo
	tvSnapshots *repository.TVSnapshotRepo
	charts      *chartHandler    // nil until WithCharts is called
	rankLists   *rankListHandler // nil until WithRankLists is called
}

// NewHandler constructs a Handler with its required repositories.
func NewHandler(
	snapshots *repository.SnapshotRepo,
	tickers *repository.TickerRepo,
	tvSnapshots *repository.TVSnapshotRepo,
) *Handler {
	return &Handler{
		snapshots:   snapshots,
		tickers:     tickers,
		tvSnapshots: tvSnapshots,
	}
}

// WithCharts attaches the chart data endpoint dependencies and returns
// the handler for fluent chaining.
func (h *Handler) WithCharts(
	candleRepo *repository.CandlesDailyRepo,
	commercialRepo *repository.CommercialReportRepo,
	enrichRepo *repository.EnrichmentRepo,
) *Handler {
	h.charts = &chartHandler{
		candleRepo:     candleRepo,
		commercialRepo: commercialRepo,
		enrichRepo:     enrichRepo,
	}
	return h
}

// WithRankLists attaches the rank list endpoint dependencies.
func (h *Handler) WithRankLists(rankListRepo *repository.RankListRepo) *Handler {
	h.rankLists = &rankListHandler{
		rankListRepo: rankListRepo,
	}
	return h
}

// RegisterRoutes attaches all API routes to mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/market-snapshots", h.postMarketSnapshot)

	// Chart data endpoint — only registered when WithCharts has been called.
	if h.charts != nil {
		mux.HandleFunc("GET /api/v1/reports/{date}/chart/{ticker}", h.charts.getChartData)
	}

	// Rank list endpoint — only registered when WithRankLists has been called.
	if h.rankLists != nil {
		mux.HandleFunc("GET /api/v1/rank-lists/{listType}/{date}", h.rankLists.getRankListByType)
	}
}

// ── POST /api/v1/market-snapshots ─────────────────────────────────────────────

// postMarketSnapshot receives a daily screener payload from tv-collector,
// stores it in market_snapshots, and upserts extracted tickers.
func (h *Handler) postMarketSnapshot(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)

	var req models.MarketSnapshotRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	// Validate date
	snapDate, err := time.Parse("2006-01-02", strings.TrimSpace(req.Date))
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid date %q — expected YYYY-MM-DD", req.Date))
		return
	}

	// Marshal screener slices to JSONB
	momentumJSON, err := json.Marshal(req.Momentum)
	if err != nil {
		writeError(w, http.StatusBadRequest, "cannot encode momentum: "+err.Error())
		return
	}
	epJSON, err := json.Marshal(req.EpisodicPivots)
	if err != nil {
		writeError(w, http.StatusBadRequest, "cannot encode episodic_pivots: "+err.Error())
		return
	}
	mlJSON, err := json.Marshal(req.MarketLeaders)
	if err != nil {
		writeError(w, http.StatusBadRequest, "cannot encode market_leaders: "+err.Error())
		return
	}

	counts := models.SnapshotRowCounts{
		Momentum:       len(req.Momentum),
		EpisodicPivots: len(req.EpisodicPivots),
		MarketLeaders:  len(req.MarketLeaders),
	}
	countsJSON, _ := json.Marshal(counts)

	snapshot := &models.MarketSnapshot{
		ID:             uuid.New(),
		SnapshotDate:   snapDate,
		Momentum:       momentumJSON,
		EpisodicPivots: epJSON,
		MarketLeaders:  mlJSON,
		RowCounts:      countsJSON,
		Open:           req.Open,
		High:           req.High,
		Low:            req.Low,
	}

	if err := h.snapshots.Upsert(r.Context(), snapshot); err != nil {
		slog.Error("store market snapshot", "date", req.Date, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to store snapshot")
		return
	}

	slog.Info("market snapshot stored",
		"date", req.Date,
		"momentum", counts.Momentum,
		"episodic_pivots", counts.EpisodicPivots,
		"market_leaders", counts.MarketLeaders,
	)

	// Best-effort: upsert tickers extracted from all three screeners.
	// Failures are logged but do not fail the request.
	allRows := make([]map[string]any, 0, len(req.Momentum)+len(req.EpisodicPivots)+len(req.MarketLeaders))
	allRows = append(allRows, req.Momentum...)
	allRows = append(allRows, req.EpisodicPivots...)
	allRows = append(allRows, req.MarketLeaders...)

	if tickers := extractTickers(allRows); len(tickers) > 0 {
		if err := h.tickers.UpsertBatch(r.Context(), tickers); err != nil {
			slog.Warn("upsert tickers from snapshot", "error", err)
		} else {
			slog.Info("tickers upserted", "count", len(tickers))
		}
	}

	// Best-effort: upsert one TradingView snapshot row per screener per ticker.
	// MarketLeaders is processed first as it carries the richest fundamentals.
	for _, batch := range []struct {
		rows   []map[string]any
		source models.ScreenerSource
	}{
		{req.MarketLeaders, models.ScreenerMarketLeaders},
		{req.EpisodicPivots, models.ScreenerEP},
		{req.Momentum, models.ScreenerMomentum},
	} {
		snaps := extractTVSnapshots(batch.rows, snapDate, batch.source)
		if len(snaps) == 0 {
			continue
		}
		if err := h.tvSnapshots.InsertBatch(r.Context(), snaps); err != nil {
			slog.Warn("upsert tv snapshots", "source", batch.source, "error", err)
		} else {
			slog.Info("tv snapshots upserted", "source", batch.source, "count", len(snaps))
		}
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"status":   "ok",
		"date":     req.Date,
		"received": counts,
	})
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// majorExchanges is the allowlist used when extracting tickers from screener
// rows.  OTC / pink-sheet names are excluded here so they never reach the
// tickers table, regardless of whether the upstream Python screener filtered
// them out.
var majorExchanges = map[string]struct{}{
	"NASDAQ":    {},
	"NYSE":      {},
	"AMEX":      {},
	"NYSE ARCA": {},
	"BATS":      {},
}

// extractTickers parses TradingView screener rows into Ticker records.
// The ticker field is in "EXCHANGE:SYMBOL" format (e.g. "NASDAQ:NVDA").
// Only tickers from majorExchanges are returned; OTC / pink-sheet names
// are silently dropped.
func extractTickers(rows []map[string]any) []models.Ticker {
	seen := make(map[string]struct{})
	var out []models.Ticker

	for _, row := range rows {
		full, _ := row["ticker"].(string) // e.g. "NASDAQ:NVDA"
		if full == "" {
			continue
		}
		exchange, symbol, _ := strings.Cut(full, ":")
		if symbol == "" {
			symbol = exchange
			exchange = ""
		}

		// Drop OTC / pink-sheet listings — only major exchanges allowed.
		if _, ok := majorExchanges[exchange]; !ok {
			continue
		}

		// Defensive copy: detach substrings from JSON decode buffer.
		symbol = strings.Clone(symbol)
		exchange = strings.Clone(exchange)

		if _, dup := seen[symbol]; dup {
			continue
		}
		seen[symbol] = struct{}{}

		name, _ := row["name"].(string)
		sector, _ := row["sector"].(string)

		out = append(out, models.Ticker{
			Ticker:   symbol,
			Name:     strings.Clone(name),
			Sector:   strings.Clone(sector),
			Exchange: exchange,
			Country:  "US",
		})
	}
	return out
}

// extractTVSnapshots converts raw TradingView screener rows into
// TradingViewSnapshotDaily records ready for upsert.
//
// Only tickers from majorExchanges are included.  Fields present in some
// screeners but not others are left nil; the DB schema allows nulls for all
// non-required columns.
func extractTVSnapshots(
	rows []map[string]any,
	snapDate time.Time,
	source models.ScreenerSource,
) []models.TradingViewSnapshotDaily {
	var out []models.TradingViewSnapshotDaily

	for _, row := range rows {
		full, _ := row["ticker"].(string)
		if full == "" {
			continue
		}
		exchange, symbol, _ := strings.Cut(full, ":")
		if symbol == "" {
			symbol = exchange
			exchange = ""
		}
		if _, ok := majorExchanges[exchange]; !ok {
			continue
		}

		// Defensive copy: detach from JSON decode buffer to prevent
		// GC "marked free object" crashes when the request body is collected.
		symbol = strings.Clone(symbol)
		exchange = strings.Clone(exchange)

		s := models.TradingViewSnapshotDaily{
			Ticker:         symbol,
			SnapshotDate:   snapDate,
			ScreenerSource: source,
		}
		if exchange != "" {
			s.Exchange = &exchange
		}

		// Helper: extract float64 field by key; returns nil when absent.
		f64 := func(key string) *float64 {
			if v, ok := row[key].(float64); ok {
				cp := v
				return &cp
			}
			return nil
		}
		// Helper: extract non-empty string field by key.
		// Uses strings.Clone to fully detach from JSON decode buffers.
		str := func(key string) *string {
			if v, ok := row[key].(string); ok && v != "" {
				c := strings.Clone(v)
				return &c
			}
			return nil
		}

		// ── Price / Liquidity ─────────────────────────────────────────────
		s.Open = f64("open")
		s.High = f64("high")
		s.Low = f64("low")
		s.Close = f64("close")
		if v, ok := row["volume"].(float64); ok {
			vi := int64(math.Round(v))
			s.Volume = &vi
		}
		s.RelativeVolume = f64("relative_volume_10d_calc")
		s.MarketCap = f64("market_cap_basic")
		if c, av := f64("close"), f64("average_volume_10d_calc"); c != nil && av != nil {
			pxv := *c * *av
			s.PriceXVolume10d = &pxv
		}
		if v, ok := row["average_volume_10d_calc"].(float64); ok {
			vi := int64(math.Round(v))
			s.AvgVolume10d = &vi
		}

		// ── Technical ─────────────────────────────────────────────────────
		s.RSI14 = f64("RSI")
		s.Perf3M = f64("Perf.3M")
		s.Perf6M = f64("Perf.6M")
		s.Price52wHigh = f64("price_52_week_high")
		s.Price52wLow = f64("price_52_week_low")
		if c, h := f64("close"), f64("price_52_week_high"); c != nil && h != nil && *h > 0 {
			d := (*c/(*h) - 1) * 100 // 0 = at high, negative = % below high
			s.Distance52wHigh = &d
		}
		s.GapPct = f64("gap")
		s.ChangePct = f64("change")

		// ── Moving averages ───────────────────────────────────────────────
		s.SMA20 = f64("SMA20")
		s.SMA50 = f64("SMA50")
		s.SMA150 = f64("SMA150")
		s.SMA200 = f64("SMA200")

		// ── Volatility ────────────────────────────────────────────────────
		s.ATR14 = f64("ATR")

		// ── Share structure ────────────────────────────────────────────────
		if v, ok := row["float_shares_outstanding"].(float64); ok {
			vi := int64(v)
			s.FloatShares = &vi
		}

		// ── Fundamentals ──────────────────────────────────────────────────
		s.EPSTTM = f64("earnings_per_share_basic_ttm")
		s.EPSGrowthYOY = f64("earnings_per_share_diluted_yoy_growth_fq")
		s.RevenueTTM = f64("total_revenue_ttm")
		s.RevenueGrowthYOY = f64("total_revenue_yoy_growth_ttm")
		s.ROE = f64("return_on_equity_fq")
		s.GrossMargin = f64("gross_margin")
		s.NetMargin = f64("net_margin")
		s.OperatingMargin = f64("operating_margin")
		// earnings_release_next_date arrives as a Unix timestamp (float64 seconds)
		// from raw TV data, or as a "YYYY-MM-DD" string from the Pydantic-validated
		// path (schemas.py _coerce_earnings_date converts timestamps to strings).
		// Handle both types to avoid nil EarningsDate in the DB.
		if earnDate, ok := row["earnings_release_next_date"]; ok && earnDate != nil {
			switch v := earnDate.(type) {
			case float64:
				if v > 0 {
					t := time.Unix(int64(v), 0).UTC()
					s.EarningsDate = &t
				}
			case string:
				if t, err := time.Parse("2006-01-02", v); err == nil {
					s.EarningsDate = &t
				}
			}
		}

		// ── Metadata ──────────────────────────────────────────────────────
		s.Sector = str("sector")

		// raw_json is NOT NULL — always store the full screener row.
		// Deep-copy the map to detach all values from the JSON decode
		// buffer before marshaling; prevents GC "pointer to free object"
		// crashes when the request body is collected during encoding.
		cloned := make(map[string]any, len(row))
		for k, v := range row {
			cloned[k] = v
		}
		if raw, err := json.Marshal(cloned); err == nil {
			s.RawJSON = json.RawMessage(raw)
		}

		out = append(out, s)
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// RecoveryMiddleware wraps h and recovers any panic in the handler.
// Instead of letting the panic abort the HTTP connection (which causes the
// client to see a "Server disconnected without sending a response" error),
// it logs the error + stack trace and writes a 500 response so the client can retry.
func RecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				slog.Error("handler panic recovered",
					"error", fmt.Sprintf("%v", err),
					"method", r.Method,
					"path", r.URL.Path,
					"stack", string(debug.Stack()),
				)
				writeError(w, http.StatusInternalServerError, "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
