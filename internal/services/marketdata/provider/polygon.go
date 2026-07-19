package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"runtime/debug"
	"time"

	"ai-stock-service/internal/metrics"
	"ai-stock-service/internal/models"
)

// buildAggsURL constructs the aggregates endpoint URL without going through
// url.Parse → modify → String(), which under high concurrency can produce
// requests that trigger "Request.RequestURI can't be set" errors.
func buildAggsURL(ticker, from, to string) string {
	return fmt.Sprintf(
		"https://api.massive.com/v2/aggs/ticker/%s/range/1/day/%s/%s?adjusted=true&sort=asc&limit=50000",
		url.PathEscape(ticker), from, to,
	)
}

// PolygonConfig holds configuration for the Massive (Polygon.io rebranded) provider.
// The API base URL is api.massive.com — Polygon.io rebranded to Massive;
// api.polygon.io is the legacy/deprecated endpoint.
type PolygonConfig struct {
	APIKey         string
	RequestsPerMin int // 5 for free tier; higher for paid plans
}

// polygonAggsResponse is a "safe" struct for decoding the Polygon.io aggregates
// (daily candles) response. By using pointers for all numeric fields, we allow
// the JSON decoder to gracefully handle `null` values from the API (which can
// occur for various reasons, like no-trade days) by setting the pointers to
// `nil`. This prevents memory corruption bugs seen in the generated client.
type polygonAggsResponse struct {
	Results []struct {
		Timestamp int64    `json:"t"`
		O         *float64 `json:"o"`
		H         *float64 `json:"h"`
		L         *float64 `json:"l"`
		C         *float64 `json:"c"`
		V         *float64 `json:"v"`
	} `json:"results"`
	NextURL *string `json:"next_url"`
}

// polygonSnapshotResponse is a "safe" struct for the snapshot endpoint.
type polygonSnapshotResponse struct {
	Ticker *struct {
		Day *struct {
			O *float64 `json:"o"`
			H *float64 `json:"h"`
			L *float64 `json:"l"`
			C *float64 `json:"c"`
			V *float64 `json:"v"`
		} `json:"day"`
	} `json:"ticker"`
}

// PolygonProvider fetches daily OHLCV data from the Massive REST API.
//
// ── Why we use raw HTTP requests instead of the generated client ─────────────
//
// The generated `massive-com/client-go/v3` library has a severe memory
// corruption bug in its JSON unmarshaling logic. Under concurrent load, it
// produces a variety of fatal errors, including SIGSEGV panics in the HTTP
// client, the JSON decoder, and even the Go runtime's garbage collector.
//
// The root cause is that the generated structs do not use pointers for numeric
// fields, and the library's unmarshaler does not safely handle `null` values
// returned by the API. This leads to memory corruption.
//
// The fix is to bypass the generated client's request/response handling
// entirely. We now:
// 1. Build and execute a raw `http.Request`.
// 2. Define "safe" response structs with pointer fields (`*float64`).
// 3. Use the standard `encoding/json` library to decode into these safe structs.
// 4. Check for `nil` pointers to safely skip incomplete data.
type PolygonProvider struct {
	apiKey     string
	httpClient *http.Client
	limiter    *Limiter
}

// NewPolygon creates a PolygonProvider.
func NewPolygon(cfg PolygonConfig) *PolygonProvider {
	rpm := cfg.RequestsPerMin
	if rpm <= 0 {
		rpm = 5
	}

	// Clone the default transport so we inherit its TLS config, proxy
	// settings, connection pooling, and dial defaults. A bare
	// &http.Transport{} gets nil for all of these, which on some
	// macOS / Go combinations causes "certificate signed by unknown
	// authority" errors because the system root pool isn't loaded.
	//
	// HTTP/2 MUST stay enabled — api.massive.com only speaks h2.
	// The previous SIGSEGV in http2ClientConn.roundTrip was mitigated by
	// buffering response bodies (io.ReadAll) instead of streaming JSON
	// decode directly from the HTTP/2 body stream.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 20 // keep idle connections for worker reuse

	return &PolygonProvider{
		apiKey:  cfg.APIKey,
		limiter: NewLimiter(rpm),
		httpClient: &http.Client{
			Timeout:   30 * time.Second,
			Transport: transport,
		},
	}
}

// Name returns the provider identifier.
func (p *PolygonProvider) Name() string { return "polygon" }

// FetchDailyCandles retrieves adjusted daily OHLCV bars for [from, to].
func (p *PolygonProvider) FetchDailyCandles(ctx context.Context, ticker string, from, to time.Time) (out []models.CandleDaily, err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("panic during FetchDailyCandles", "ticker", ticker, "panic", r, "stack", string(debug.Stack()))
			err = fmt.Errorf("recovered from panic in FetchDailyCandles for %s: %v", ticker, r)
		}
	}()

	if err := p.limiter.Wait(ctx); err != nil {
		return nil, err
	}

	reqURL := buildAggsURL(ticker, from.Format("2006-01-02"), to.Format("2006-01-02"))

	// Inline buffered GET — we need to handle 404 (→ empty slice) which
	// doGetRaw does not support (it errors on any non-200).
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("polygon aggregates %s: create request: %w", ticker, err)
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	start := time.Now()
	resp, err := p.httpClient.Do(req)
	if err != nil {
		metrics.MarketDataFetchesTotal.WithLabelValues(p.Name(), "failure").Inc()
		return nil, fmt.Errorf("polygon aggregates %s: %w", ticker, err)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 10<<20)) // 10 MB
	_ = resp.Body.Close()
	if readErr != nil {
		metrics.MarketDataFetchesTotal.WithLabelValues(p.Name(), "failure").Inc()
		return nil, fmt.Errorf("polygon aggregates %s: read body: %w", ticker, readErr)
	}

	if resp.StatusCode == http.StatusNotFound {
		// 404 is treated as empty result, not a failure
		metrics.MarketDataFetchesTotal.WithLabelValues(p.Name(), "success").Inc()
		metrics.MarketDataFetchDuration.WithLabelValues(p.Name()).Observe(time.Since(start).Seconds())
		return []models.CandleDaily{}, nil
	}
	if resp.StatusCode != http.StatusOK {
		metrics.MarketDataFetchesTotal.WithLabelValues(p.Name(), "failure").Inc()
		return nil, fmt.Errorf("polygon aggregates %s: HTTP %d: %s", ticker, resp.StatusCode, string(body))
	}

	// Safely decode the response.
	var aggs polygonAggsResponse
	if err := json.Unmarshal(body, &aggs); err != nil {
		metrics.MarketDataFetchesTotal.WithLabelValues(p.Name(), "failure").Inc()
		return nil, fmt.Errorf("polygon aggregates %s: decode response: %w", ticker, err)
	}

	// Process results, skipping any bars with missing data.
	provider := p.Name()
	for _, b := range aggs.Results {
		if b.O == nil || b.H == nil || b.L == nil || b.C == nil || b.V == nil {
			continue // Skip incomplete/empty trading day data.
		}
		date := time.UnixMilli(b.Timestamp).UTC().Truncate(24 * time.Hour)
		adj := *b.C
		out = append(out, models.CandleDaily{
			Ticker:        ticker,
			Date:          date,
			Open:          *b.O,
			High:          *b.H,
			Low:           *b.L,
			Close:         *b.C,
			AdjustedClose: &adj,
			Volume:        int64(*b.V),
			Provider:      provider,
		})
	}

	if aggs.NextURL != nil && *aggs.NextURL != "" {
		slog.Warn("polygon aggregates: result truncated at page limit", "ticker", ticker)
	}

	metrics.MarketDataFetchesTotal.WithLabelValues(p.Name(), "success").Inc()
	metrics.MarketDataFetchDuration.WithLabelValues(p.Name()).Observe(time.Since(start).Seconds())
	return out, nil
}

// FetchSnapshot returns a single CandleDaily built from the snapshot endpoint.
func (p *PolygonProvider) FetchSnapshot(ctx context.Context, ticker string) (out *models.CandleDaily, err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("panic during FetchSnapshot", "ticker", ticker, "panic", r, "stack", string(debug.Stack()))
			err = fmt.Errorf("recovered from panic in FetchSnapshot for %s: %v", ticker, r)
		}
	}()

	if err := p.limiter.Wait(ctx); err != nil {
		return nil, err
	}

	start := time.Now()
	reqURL := fmt.Sprintf("https://api.massive.com/v2/stocks/%s/snapshot", url.PathEscape(ticker))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("polygon snapshot %s: create request: %w", ticker, err)
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		metrics.MarketDataFetchesTotal.WithLabelValues(p.Name(), "failure").Inc()
		return nil, fmt.Errorf("polygon snapshot %s: %w", ticker, err)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MB
	_ = resp.Body.Close()
	if readErr != nil {
		metrics.MarketDataFetchesTotal.WithLabelValues(p.Name(), "failure").Inc()
		return nil, fmt.Errorf("polygon snapshot %s: read body: %w", ticker, readErr)
	}

	if resp.StatusCode == http.StatusNotFound {
		metrics.MarketDataFetchesTotal.WithLabelValues(p.Name(), "success").Inc()
		metrics.MarketDataFetchDuration.WithLabelValues(p.Name()).Observe(time.Since(start).Seconds())
		return nil, nil //nolint:nilnil // 404 means ticker not found; return nil without error
	}
	if resp.StatusCode != http.StatusOK {
		metrics.MarketDataFetchesTotal.WithLabelValues(p.Name(), "failure").Inc()
		return nil, fmt.Errorf("polygon snapshot %s: HTTP %d: %s", ticker, resp.StatusCode, string(body))
	}

	var snapshot polygonSnapshotResponse
	if err := json.Unmarshal(body, &snapshot); err != nil {
		metrics.MarketDataFetchesTotal.WithLabelValues(p.Name(), "failure").Inc()
		return nil, fmt.Errorf("polygon snapshot %s: decode response: %w", ticker, err)
	}

	if snapshot.Ticker == nil || snapshot.Ticker.Day == nil {
		metrics.MarketDataFetchesTotal.WithLabelValues(p.Name(), "success").Inc()
		metrics.MarketDataFetchDuration.WithLabelValues(p.Name()).Observe(time.Since(start).Seconds())
		return nil, nil //nolint:nilnil // snapshot has no day data; return nil without error
	}
	day := snapshot.Ticker.Day
	if day.C == nil || day.V == nil || *day.C == 0 || *day.V == 0 {
		metrics.MarketDataFetchesTotal.WithLabelValues(p.Name(), "success").Inc()
		metrics.MarketDataFetchDuration.WithLabelValues(p.Name()).Observe(time.Since(start).Seconds())
		return nil, nil //nolint:nilnil // No intraday data yet; return nil without error
	}
	if day.O == nil || day.H == nil || day.L == nil {
		metrics.MarketDataFetchesTotal.WithLabelValues(p.Name(), "success").Inc()
		metrics.MarketDataFetchDuration.WithLabelValues(p.Name()).Observe(time.Since(start).Seconds())
		return nil, nil //nolint:nilnil // Incomplete data.
	}

	metrics.MarketDataFetchesTotal.WithLabelValues(p.Name(), "success").Inc()
	metrics.MarketDataFetchDuration.WithLabelValues(p.Name()).Observe(time.Since(start).Seconds())
	today := time.Now().UTC().Truncate(24 * time.Hour)
	adj := *day.C
	return &models.CandleDaily{
		Ticker:        ticker,
		Date:          today,
		Open:          *day.O,
		High:          *day.H,
		Low:           *day.L,
		Close:         *day.C,
		AdjustedClose: &adj,
		Volume:        int64(*day.V),
		Provider:      p.Name(),
	}, nil
}
