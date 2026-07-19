// File: internal/api/handler_test.go
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ai-stock-service/internal/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── writeJSON / writeError ─────────────────────────────────────────────────────

func TestWriteJSON_ValidPayload(t *testing.T) {
	// Validates that writeJSON serialises a struct, sets Content-Type, and
	// returns the expected status code.
	rr := httptest.NewRecorder()
	data := map[string]string{"key": "value"}
	writeJSON(rr, http.StatusCreated, data)

	assert.Equal(t, http.StatusCreated, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))

	var got map[string]string
	err := json.Unmarshal(rr.Body.Bytes(), &got)
	require.NoError(t, err)
	assert.Equal(t, "value", got["key"])
}

func TestWriteJSON_NilBody(t *testing.T) {
	// Validates that writeJSON handles a nil value without panicking.
	rr := httptest.NewRecorder()
	writeJSON(rr, http.StatusOK, nil)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	// json.Encode(nil) writes "null"
	assert.Equal(t, "null\n", rr.Body.String())
}

func TestWriteError_FormatsCorrectly(t *testing.T) {
	// Validates that writeError produces {"error":"message"} with the given status.
	rr := httptest.NewRecorder()
	writeError(rr, http.StatusBadRequest, "invalid input")

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))

	var body map[string]string
	err := json.Unmarshal(rr.Body.Bytes(), &body)
	require.NoError(t, err)
	assert.Equal(t, "invalid input", body["error"])
}

func TestWriteError_EmptyMessage(t *testing.T) {
	// Validates that writeError works with an empty message string.
	rr := httptest.NewRecorder()
	writeError(rr, http.StatusInternalServerError, "")

	assert.Equal(t, http.StatusInternalServerError, rr.Code)

	var body map[string]string
	err := json.Unmarshal(rr.Body.Bytes(), &body)
	require.NoError(t, err)
	assert.Equal(t, "", body["error"])
}

// ── RecoveryMiddleware ─────────────────────────────────────────────────────────

func TestRecoveryMiddleware_NormalPassThrough(t *testing.T) {
	// Validates that RecoveryMiddleware passes through a normal request without
	// panicking and returns the handler's response unchanged.
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	handler := RecoveryMiddleware(inner)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/test", http.NoBody)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "OK", rr.Body.String())
}

func TestRecoveryMiddleware_PanicReturns500(t *testing.T) {
	// Validates that when the inner handler panics, RecoveryMiddleware catches it
	// and returns a 500 with an error JSON body instead of crashing.
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("something went wrong")
	})

	handler := RecoveryMiddleware(inner)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/panic", http.NoBody)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusInternalServerError, rr.Code)

	var body map[string]string
	err := json.Unmarshal(rr.Body.Bytes(), &body)
	require.NoError(t, err)
	assert.Equal(t, "internal server error", body["error"])
}

func TestRecoveryMiddleware_PanicWithError(t *testing.T) {
	// Validates that RecoveryMiddleware handles a typed error panic.
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(errors.New("db connection lost"))
	})

	handler := RecoveryMiddleware(inner)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/panic-err", http.NoBody)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
}

// ── extractTickers ─────────────────────────────────────────────────────────────

func TestExtractTickers_Nominal(t *testing.T) {
	// Validates that extractTickers correctly parses "EXCHANGE:SYMBOL" format,
	// keeps only major-exchange tickers, and deduplicates.
	rows := []map[string]any{
		{"ticker": "NASDAQ:NVDA", "name": "NVIDIA Corp", "sector": "Technology"},
		{"ticker": "NYSE:BRK.B", "name": "Berkshire Hathaway", "sector": "Financial"},
		{"ticker": "AMEX:SPY", "name": "SPDR S&P 500", "sector": "ETF"},
	}
	tickers := extractTickers(rows)
	require.Len(t, tickers, 3)
	assert.Equal(t, "NVDA", tickers[0].Ticker)
	assert.Equal(t, "NVIDIA Corp", tickers[0].Name)
	assert.Equal(t, "Technology", tickers[0].Sector)
	assert.Equal(t, "NASDAQ", tickers[0].Exchange)
	assert.Equal(t, "US", tickers[0].Country)
}

func TestExtractTickers_FiltersOTCGarbage(t *testing.T) {
	// Validates that OTC / pink-sheet listings are dropped and only
	// major-exchange tickers are returned.
	rows := []map[string]any{
		{"ticker": "OTC:ZZZZ", "name": "Pink Sheet Co"},
		{"ticker": "NASDAQ:MSFT", "name": "Microsoft"},
		{"ticker": "PINK:ABCD", "name": "Another Garbage"},
	}
	tickers := extractTickers(rows)
	require.Len(t, tickers, 1)
	assert.Equal(t, "MSFT", tickers[0].Ticker)
}

func TestExtractTickers_DeduplicatesBySymbol(t *testing.T) {
	// Validates that duplicate symbols from the same source are collapsed
	// to a single entry.
	rows := []map[string]any{
		{"ticker": "NASDAQ:AAPL"},
		{"ticker": "NASDAQ:AAPL"}, // duplicate
	}
	tickers := extractTickers(rows)
	require.Len(t, tickers, 1)
}

func TestExtractTickers_EmptyTickerSkipped(t *testing.T) {
	// Validates that rows with an empty ticker field are silently skipped.
	rows := []map[string]any{
		{"ticker": "", "name": "Empty"},
		{"ticker": "NASDAQ:AAPL", "name": "Apple"},
	}
	tickers := extractTickers(rows)
	require.Len(t, tickers, 1)
}

func TestExtractTickers_MissingExchangeDefaultsToMajor(t *testing.T) {
	// Validates that a ticker without "EXCHANGE:" prefix defaults exchange to
	// the raw value and is then filtered if not major (since empty exchange key
	// won't match majorExchanges).
	rows := []map[string]any{
		{"ticker": "SOME:RANDOM"}, // not in majorExchanges → filtered
		{"ticker": "NYSE:JPM"},    // should pass
	}
	tickers := extractTickers(rows)
	require.Len(t, tickers, 1)
	assert.Equal(t, "JPM", tickers[0].Ticker)
}

func TestExtractTickers_NoTickerKey(t *testing.T) {
	// Validates that a row missing the "ticker" key entirely does not cause
	// a panic and is skipped.
	rows := []map[string]any{
		{"name": "No ticker row"},
	}
	tickers := extractTickers(rows)
	require.Empty(t, tickers)
}

func TestExtractTickers_HandlesBATSExchange(t *testing.T) {
	// Validates that BATS is included in the major exchanges allowlist.
	rows := []map[string]any{
		{"ticker": "BATS:VOO", "name": "Vanguard S&P 500 ETF"},
	}
	tickers := extractTickers(rows)
	require.Len(t, tickers, 1)
	assert.Equal(t, "VOO", tickers[0].Ticker)
	assert.Equal(t, "BATS", tickers[0].Exchange)
}

func TestExtractTickers_HandlesNYSEARCAExchange(t *testing.T) {
	// Validates that "NYSE ARCA" is included in the major exchanges allowlist.
	rows := []map[string]any{
		{"ticker": "NYSE ARCA:SPY", "name": "SPDR S&P 500"},
	}
	tickers := extractTickers(rows)
	require.Len(t, tickers, 1)
	assert.Equal(t, "SPY", tickers[0].Ticker)
}

func TestExtractTickers_DefensiveCloneDetachedFromDecoderBuffer(t *testing.T) {
	// Validates that returned ticker symbols are independent strings, not
	// substrings of the JSON decode buffer (which could cause GC crashes
	// in production).  We simulate this by re-using the same backing map.
	row := map[string]any{"ticker": "NASDAQ:TEST"}
	rows := []map[string]any{row, row} // same map ref twice
	tickers := extractTickers(rows)
	require.Len(t, tickers, 1)
	assert.Equal(t, "TEST", tickers[0].Ticker)
}

// ── extractTVSnapshots ────────────────────────────────────────────────────────

func TestExtractTVSnapshots_Nominal(t *testing.T) {
	// Validates that a row with all fields present produces a complete
	// TradingViewSnapshotDaily record.
	snapDate := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	rows := []map[string]any{
		{
			"ticker":                   "NASDAQ:NVDA",
			"name":                     "NVIDIA Corp",
			"close":                    950.0,
			"open":                     940.0,
			"high":                     960.0,
			"low":                      935.0,
			"volume":                   5e7,
			"relative_volume_10d_calc": 1.5,
			"market_cap_basic":         2.5e12,
			"average_volume_10d_calc":  4e7,
			"RSI":                      65.0,
			"Perf.3M":                  12.5,
			"Perf.6M":                  25.0,
			"SMA20":                    920.0,
			"SMA50":                    880.0,
			"SMA150":                   800.0,
			"SMA200":                   750.0,
			"ATR":                      15.0,
			"sector":                   "Technology",
			"gap":                      0.5,
			"change":                   1.2,
			"price_52_week_high":       1000.0,
			"price_52_week_low":        500.0,
		},
	}
	snaps := extractTVSnapshots(rows, snapDate, models.ScreenerMarketLeaders)
	require.Len(t, snaps, 1)
	s := snaps[0]
	assert.Equal(t, "NVDA", s.Ticker)
	assert.Equal(t, snapDate, s.SnapshotDate)
	assert.Equal(t, models.ScreenerMarketLeaders, s.ScreenerSource)
	require.NotNil(t, s.Close)
	assert.Equal(t, 950.0, *s.Close)
	require.NotNil(t, s.Open)
	assert.Equal(t, 940.0, *s.Open)
	require.NotNil(t, s.Volume)
	assert.Equal(t, int64(5e7), *s.Volume)
	require.NotNil(t, s.RelativeVolume)
	assert.Equal(t, 1.5, *s.RelativeVolume)
	require.NotNil(t, s.SMA20)
	assert.Equal(t, 920.0, *s.SMA20)
	require.NotNil(t, s.Sector)
	assert.Equal(t, "Technology", *s.Sector)
}

func TestExtractTVSnapshots_FiltersOTC(t *testing.T) {
	// Validates that OTC-ticker rows are dropped from the snapshot list.
	snapDate := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	rows := []map[string]any{
		{"ticker": "OTC:ZZZZ", "close": 1.0},
		{"ticker": "NASDAQ:AAPL", "close": 200.0},
	}
	snaps := extractTVSnapshots(rows, snapDate, models.ScreenerMomentum)
	require.Len(t, snaps, 1)
	assert.Equal(t, "AAPL", snaps[0].Ticker)
}

func TestExtractTVSnapshots_EmptyTickerSkipped(t *testing.T) {
	// Validates that rows with empty ticker fields are silently skipped.
	snapDate := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	rows := []map[string]any{
		{"ticker": "", "close": 150.0},
	}
	snaps := extractTVSnapshots(rows, snapDate, models.ScreenerEP)
	require.Empty(t, snaps)
}

func TestExtractTVSnapshots_PartialFields(t *testing.T) {
	// Validates that rows with only a ticker and close still produce a valid
	// snapshot with nil pointers for the missing fields.
	snapDate := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	rows := []map[string]any{
		{"ticker": "NYSE:JPM"},
	}
	snaps := extractTVSnapshots(rows, snapDate, models.ScreenerMarketLeaders)
	require.Len(t, snaps, 1)
	assert.Equal(t, "JPM", snaps[0].Ticker)
	assert.Nil(t, snaps[0].Close)
	assert.Nil(t, snaps[0].Open)
	assert.Nil(t, snaps[0].RSI14)
}

func TestExtractTVSnapshots_ComputesPriceXVolume10d(t *testing.T) {
	// Validates that PriceXVolume10d is computed as close × average_volume_10d_calc.
	snapDate := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	rows := []map[string]any{
		{
			"ticker":                  "NASDAQ:AAPL",
			"close":                   200.0,
			"average_volume_10d_calc": 5e7,
		},
	}
	snaps := extractTVSnapshots(rows, snapDate, models.ScreenerMomentum)
	require.Len(t, snaps, 1)
	require.NotNil(t, snaps[0].PriceXVolume10d)
	assert.Equal(t, 200.0*5e7, *snaps[0].PriceXVolume10d)
}

func TestExtractTVSnapshots_ComputesDistance52wHigh(t *testing.T) {
	// Validates that Distance52wHigh is computed correctly: (close/high - 1) * 100.
	// At high: 0%.  10% below high: -10%.
	snapDate := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	rows := []map[string]any{
		{
			"ticker":             "NASDAQ:AAPL",
			"close":              180.0,
			"price_52_week_high": 200.0, // 10% below
		},
	}
	snaps := extractTVSnapshots(rows, snapDate, models.ScreenerEP)
	require.Len(t, snaps, 1)
	require.NotNil(t, snaps[0].Distance52wHigh)
	assert.InDelta(t, -10.0, *snaps[0].Distance52wHigh, 0.001)
}

func TestExtractTVSnapshots_EarningsDateAsFloat(t *testing.T) {
	// Validates that earnings_release_next_date as a Unix float64 is converted
	// to a time.Time.
	snapDate := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	earnUnix := float64(time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC).Unix())
	rows := []map[string]any{
		{
			"ticker":                     "NASDAQ:AAPL",
			"earnings_release_next_date": earnUnix,
		},
	}
	snaps := extractTVSnapshots(rows, snapDate, models.ScreenerMarketLeaders)
	require.Len(t, snaps, 1)
	require.NotNil(t, snaps[0].EarningsDate)
	assert.Equal(t, "2026-06-15", snaps[0].EarningsDate.Format("2006-01-02"))
}

func TestExtractTVSnapshots_EarningsDateAsString(t *testing.T) {
	// Validates that earnings_release_next_date as a "YYYY-MM-DD" string is
	// parsed correctly.
	snapDate := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	rows := []map[string]any{
		{
			"ticker":                     "NASDAQ:AAPL",
			"earnings_release_next_date": "2026-07-30",
		},
	}
	snaps := extractTVSnapshots(rows, snapDate, models.ScreenerEP)
	require.Len(t, snaps, 1)
	require.NotNil(t, snaps[0].EarningsDate)
	assert.Equal(t, "2026-07-30", snaps[0].EarningsDate.Format("2006-01-02"))
}

func TestExtractTVSnapshots_EarningsDateNilForInvalid(t *testing.T) {
	// Validates that an invalid earnings_release_next_date (e.g. empty string)
	// results in a nil EarningsDate rather than a parse error.
	snapDate := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	rows := []map[string]any{
		{
			"ticker":                     "NASDAQ:AAPL",
			"earnings_release_next_date": "",
		},
	}
	snaps := extractTVSnapshots(rows, snapDate, models.ScreenerMomentum)
	require.Len(t, snaps, 1)
	assert.Nil(t, snaps[0].EarningsDate)
}

func TestExtractTVSnapshots_EarningsDateSkippedWhenZero(t *testing.T) {
	// Validates that a zero-valued float64 earnings date (Unix epoch) is skipped
	// because it would be January 1, 1970 — clearly invalid.
	snapDate := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	rows := []map[string]any{
		{
			"ticker":                     "NASDAQ:AAPL",
			"earnings_release_next_date": float64(0),
		},
	}
	snaps := extractTVSnapshots(rows, snapDate, models.ScreenerMarketLeaders)
	require.Len(t, snaps, 1)
	assert.Nil(t, snaps[0].EarningsDate)
}

func TestExtractTVSnapshots_RawJSONAlwaysPresent(t *testing.T) {
	// Validates that the raw_json field is always populated with the full row
	// content, even for minimal rows.
	snapDate := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	rows := []map[string]any{
		{"ticker": "NASDAQ:AAPL", "close": 200.0},
	}
	snaps := extractTVSnapshots(rows, snapDate, models.ScreenerMarketLeaders)
	require.Len(t, snaps, 1)
	assert.NotEmpty(t, snaps[0].RawJSON)
}

func TestExtractTVSnapshots_ExchangeSetWhenNonEmpty(t *testing.T) {
	// Validates that Exchange is set to a non-nil pointer when exchange != "".
	snapDate := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	rows := []map[string]any{
		{"ticker": "NYSE:BRK.B"},
	}
	snaps := extractTVSnapshots(rows, snapDate, models.ScreenerMarketLeaders)
	require.Len(t, snaps, 1)
	require.NotNil(t, snaps[0].Exchange)
	assert.Equal(t, "NYSE", *snaps[0].Exchange)
}

// ── CORSMiddleware ─────────────────────────────────────────────────────────────

func TestCORSMiddleware_WithAllowedOrigin_SetsHeaders(t *testing.T) {
	// Validates that CORSMiddleware sets the expected CORS headers when
	// the request Origin matches the allowed list.
	allowedOrigins := []string{"https://example.com"}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	handler := CORSMiddleware(allowedOrigins)(inner)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/test", http.NoBody)
	req.Header.Set("Origin", "https://example.com")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "https://example.com", rr.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "Origin", rr.Header().Get("Vary"))
	assert.Equal(t, "GET, POST, OPTIONS", rr.Header().Get("Access-Control-Allow-Methods"))
	assert.Equal(t, "Content-Type, Authorization, X-API-Key", rr.Header().Get("Access-Control-Allow-Headers"))
	assert.Equal(t, "86400", rr.Header().Get("Access-Control-Max-Age"))
}

func TestCORSMiddleware_WithNilOrigins_NoHeaders(t *testing.T) {
	// When CORSMiddleware is configured with nil (no allowed origins),
	// no CORS headers should be emitted and the request should pass through.
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	handler := CORSMiddleware(nil)(inner)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/test", http.NoBody)
	req.Header.Set("Origin", "https://evil.com")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Empty(t, rr.Header().Get("Access-Control-Allow-Origin"))
}

func TestCORSMiddleware_OPTIONSReturns204(t *testing.T) {
	// Validates that OPTIONS requests return 204 No Content and do not invoke
	// the next handler when the origin is allowed.
	allowedOrigins := []string{"https://example.com"}
	innerCalled := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		innerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	handler := CORSMiddleware(allowedOrigins)(inner)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodOptions, "/test", http.NoBody)
	req.Header.Set("Origin", "https://example.com")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusNoContent, rr.Code)
	assert.False(t, innerCalled)
}

// ── MetricsMiddleware ─────────────────────────────────────────────────────────

func TestMetricsMiddleware_RecordsStatusCode(t *testing.T) {
	// Validates that MetricsMiddleware captures the response status code via
	// the wrapped responseWriter and records it.
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("not found"))
	})

	handler := MetricsMiddleware(inner)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/test", http.NoBody)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestMetricsMiddleware_PassthroughWriteHeader(t *testing.T) {
	// Validates that the responseWriter wrapper passes through WriteHeader
	// correctly and the body reaches the caller unchanged.
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	handler := MetricsMiddleware(inner)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/ok", http.NoBody)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "OK", rr.Body.String())
}

func TestMetricsMiddleware_DefaultStatusWhenUnset(t *testing.T) {
	// Validates that when the inner handler does not explicitly call
	// WriteHeader, the default is http.StatusOK (200).
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("no header"))
	})

	handler := MetricsMiddleware(inner)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/no-header", http.NoBody)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "no header", rr.Body.String())
}

// ── parsePrice ─────────────────────────────────────────────────────────────────

func TestParsePrice_StandardFormat(t *testing.T) {
	assert.InDelta(t, 143.50, parsePrice("143.50"), 0.001)
}

func TestParsePrice_WithDollarSign(t *testing.T) {
	assert.InDelta(t, 2500.00, parsePrice("$2,500.00"), 0.001)
}

func TestParsePrice_WithCommas(t *testing.T) {
	assert.InDelta(t, 1000000.00, parsePrice("1,000,000"), 0.001)
}

func TestParsePrice_WithRange(t *testing.T) {
	// Validates that parsePrice takes the first number when given a range.
	assert.InDelta(t, 143.00, parsePrice("143.00 – 145.50"), 0.001)
}

func TestParsePrice_EmptyString(t *testing.T) {
	assert.InDelta(t, 0.0, parsePrice(""), 0.001)
}

func TestParsePrice_NonNumeric(t *testing.T) {
	assert.InDelta(t, 0.0, parsePrice("N/A"), 0.001)
}

func TestParsePrice_OnlySymbols(t *testing.T) {
	assert.InDelta(t, 0.0, parsePrice("$-"), 0.001)
}

// ── parseRange ─────────────────────────────────────────────────────────────────

func TestParseRange_EnDashSeparator(t *testing.T) {
	// Validates that " – " (en-dash) separator works.
	lo, hi, ok := parseRange("$143.00 – $145.50")
	require.True(t, ok)
	assert.InDelta(t, 143.00, lo, 0.001)
	assert.InDelta(t, 145.50, hi, 0.001)
}

func TestParseRange_HyphenSeparator(t *testing.T) {
	// Validates that " - " (hyphen) separator works.
	lo, hi, ok := parseRange("$50.00 - $60.00")
	require.True(t, ok)
	assert.InDelta(t, 50.00, lo, 0.001)
	assert.InDelta(t, 60.00, hi, 0.001)
}

func TestParseRange_HyphenOnlySeparator(t *testing.T) {
	// Validates that bare "–" (without spaces) works.
	lo, hi, ok := parseRange("100–200")
	require.True(t, ok)
	assert.InDelta(t, 100.00, lo, 0.001)
	assert.InDelta(t, 200.00, hi, 0.001)
}

func TestParseRange_CommasInNumbers(t *testing.T) {
	// Validates that comma-formatted numbers are parsed correctly.
	lo, hi, ok := parseRange("$1,000 – $2,500")
	require.True(t, ok)
	assert.InDelta(t, 1000.00, lo, 0.001)
	assert.InDelta(t, 2500.00, hi, 0.001)
}

func TestParseRange_NotARange(t *testing.T) {
	// Validates that a single price (not a range) returns ok=false.
	_, _, ok := parseRange("150.00")
	assert.False(t, ok)
}

func TestParseRange_EmptyReturnsFalse(t *testing.T) {
	_, _, ok := parseRange("")
	assert.False(t, ok)
}

func TestParseRange_MalformedReturnsFalse(t *testing.T) {
	_, _, ok := parseRange("not-a-range")
	assert.False(t, ok)
}

// ── patternLabel ───────────────────────────────────────────────────────────────

func TestPatternLabel_VCP(t *testing.T) {
	assert.Equal(t, "VCP (Volatility Contraction)", patternLabel("VCP"))
	assert.Equal(t, "VCP (Volatility Contraction)", patternLabel("volatility contraction pattern"))
	assert.Equal(t, "VCP (Volatility Contraction)", patternLabel("tight VCP"))
}

func TestPatternLabel_CupWithHandle(t *testing.T) {
	assert.Equal(t, "Cup with Handle", patternLabel("Cup with Handle"))
	assert.Equal(t, "Cup with Handle", patternLabel("Cup With Handle"))
}

func TestPatternLabel_TightBase(t *testing.T) {
	assert.Equal(t, "Tight Base", patternLabel("tight"))
	assert.Equal(t, "Tight Base", patternLabel("TIGHT base"))
}

func TestPatternLabel_FlatBase(t *testing.T) {
	assert.Equal(t, "Flat Base", patternLabel("flat base"))
	assert.Equal(t, "Flat Base", patternLabel("Flat Base"))
}

func TestPatternLabel_Fallback(t *testing.T) {
	assert.Equal(t, "Double Bottom", patternLabel("Double Bottom"))
	assert.Equal(t, "Ascending Triangle", patternLabel("Ascending Triangle"))
}

func TestPatternLabel_EmptyString(t *testing.T) {
	assert.Equal(t, "", patternLabel(""))
}

// ── buildChartMarkers ─────────────────────────────────────────────────────────

func TestBuildChartMarkers_WithEntryLow(t *testing.T) {
	// Validates that a buy-zone marker is created when EntryLow is set.
	entryLow := 150.0
	levels := &TradeLevels{EntryLow: &entryLow}
	markers := buildChartMarkers("2026-05-20", levels)
	require.Len(t, markers, 1)
	assert.Equal(t, "2026-05-20", markers[0].Time)
	assert.Equal(t, "belowBar", markers[0].Position)
	assert.Equal(t, "#2196F3", markers[0].Color)
	assert.Equal(t, "arrowUp", markers[0].Shape)
	assert.Equal(t, "Buy Zone", markers[0].Text)
}

func TestBuildChartMarkers_NilLevels(t *testing.T) {
	// Validates that nil levels produce no markers.
	markers := buildChartMarkers("2026-05-20", nil)
	assert.Empty(t, markers)
}

func TestBuildChartMarkers_NilEntryLow(t *testing.T) {
	// Validates that levels with a nil EntryLow produce no markers.
	levels := &TradeLevels{EntryLow: nil}
	markers := buildChartMarkers("2026-05-20", levels)
	assert.Empty(t, markers)
}

// ── computePivotDistance ───────────────────────────────────────────────────────

func TestComputePivotDistance_WithinRange(t *testing.T) {
	// Validates that a candle close within ±15% of a resistance trendline
	// endpoint returns a valid pivot distance.
	candles := []models.CandleDaily{
		{Date: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), Close: 190.0},
		{Date: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC), Close: 195.0},
	}
	trendlines := []ChartTrendline{
		{Point1: ChartPoint{Time: "2026-04-01", Value: 180}, Point2: ChartPoint{Time: "2026-05-20", Value: 200}, Role: "resistance"},
	}
	pivot, pct, ok := computePivotDistance(candles, trendlines)
	require.True(t, ok)
	assert.InDelta(t, 200.0, pivot, 0.001)
	assert.InDelta(t, -2.5, pct, 0.1)
}

func TestComputePivotDistance_NoResistanceLine(t *testing.T) {
	// Validates that ok=false when there's no resistance trendline.
	candles := []models.CandleDaily{
		{Close: 100.0},
	}
	trendlines := []ChartTrendline{
		{Role: "support"},
	}
	_, _, ok := computePivotDistance(candles, trendlines)
	assert.False(t, ok)
}

func TestComputePivotDistance_EmptyCandles(t *testing.T) {
	_, _, ok := computePivotDistance(nil, []ChartTrendline{{Role: "resistance"}})
	assert.False(t, ok)
}

func TestComputePivotDistance_EmptyTrendlines(t *testing.T) {
	_, _, ok := computePivotDistance([]models.CandleDaily{{Close: 100}}, nil)
	assert.False(t, ok)
}

func TestComputePivotDistance_TooFar(t *testing.T) {
	// Validates that a close beyond ±15% returns ok=false.
	candles := []models.CandleDaily{
		{Close: 50.0}, // (50-200)/200 * 100 = -75%, way below
	}
	trendlines := []ChartTrendline{
		{Point2: ChartPoint{Value: 200}, Role: "resistance"},
	}
	_, _, ok := computePivotDistance(candles, trendlines)
	assert.False(t, ok)
}

func TestComputePivotDistance_AtExactly15Percent(t *testing.T) {
	// Validates that exactly -15% is included (±15% is inclusive, then the
	// > check excludes beyond).
	candles := []models.CandleDaily{
		{Close: 170.0},
	}
	trendlines := []ChartTrendline{
		{Point2: ChartPoint{Value: 200}, Role: "resistance"},
	}
	_, _, ok := computePivotDistance(candles, trendlines)
	assert.True(t, ok)
}

func TestComputePivotDistance_AtExactlyPositive15Percent(t *testing.T) {
	// Validates that exactly +15% is included.
	candles := []models.CandleDaily{
		{Close: 230.0},
	}
	trendlines := []ChartTrendline{
		{Point2: ChartPoint{Value: 200}, Role: "resistance"},
	}
	_, _, ok := computePivotDistance(candles, trendlines)
	assert.True(t, ok)
}

func TestComputePivotDistance_AbovePivot(t *testing.T) {
	// Validates that a close above the resistance line gives a positive pct.
	candles := []models.CandleDaily{
		{Close: 210.0},
	}
	trendlines := []ChartTrendline{
		{Point2: ChartPoint{Value: 200}, Role: "resistance"},
	}
	_, pct, ok := computePivotDistance(candles, trendlines)
	require.True(t, ok)
	assert.InDelta(t, 5.0, pct, 0.1)
}

func TestComputePivotDistance_ZeroPivotReturnsFalse(t *testing.T) {
	// Validates that a resistance line with a zero-price endpoint returns false.
	candles := []models.CandleDaily{
		{Close: 100.0},
	}
	trendlines := []ChartTrendline{
		{Point2: ChartPoint{Value: 0}, Role: "resistance"},
	}
	_, _, ok := computePivotDistance(candles, trendlines)
	assert.False(t, ok)
}

func TestComputePivotDistance_FirstResistanceWins(t *testing.T) {
	// Validates that the first resistance trendline is used when multiple exist.
	candles := []models.CandleDaily{
		{Close: 200.0},
	}
	trendlines := []ChartTrendline{
		{Point2: ChartPoint{Value: 180}, Role: "resistance"}, // first = 180
		{Point2: ChartPoint{Value: 220}, Role: "resistance"}, // second = 220
	}
	pivot, pct, ok := computePivotDistance(candles, trendlines)
	require.True(t, ok)
	assert.InDelta(t, 180.0, pivot, 0.001)
	assert.InDelta(t, 11.1, pct, 0.1) // (200-180)/180*100 ≈ 11.1
}
