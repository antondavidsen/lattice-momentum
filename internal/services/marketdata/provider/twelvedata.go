package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"ai-stock-service/internal/metrics"
	"ai-stock-service/internal/models"
)

const twelveDataBaseURL = "https://api.twelvedata.com"

// TwelveDataConfig holds the configuration for the TwelveData provider.
type TwelveDataConfig struct {
	APIKey         string
	RequestsPerMin int // 8 for free tier; higher for paid plans
}

// TwelveDataProvider fetches daily OHLCV data from the TwelveData time_series API.
// The free tier does not supply split/dividend adjusted prices; adjusted_close
// is stored as NULL for those bars.
type TwelveDataProvider struct {
	apiKey  string
	client  *http.Client
	limiter *Limiter
}

// NewTwelveData creates a TwelveDataProvider. If RequestsPerMin is 0, defaults to 8 (free tier).
func NewTwelveData(cfg TwelveDataConfig) *TwelveDataProvider {
	rpm := cfg.RequestsPerMin
	if rpm <= 0 {
		rpm = 8
	}
	return &TwelveDataProvider{
		apiKey:  cfg.APIKey,
		client:  &http.Client{Timeout: 30 * time.Second},
		limiter: NewLimiter(rpm),
	}
}

// Name returns the provider name for metrics and logging.
func (p *TwelveDataProvider) Name() string { return "twelvedata" }

// FetchDailyCandles retrieves daily bars from the TwelveData /time_series endpoint.
// Results are requested in ascending order (order=ASC).
func (p *TwelveDataProvider) FetchDailyCandles(ctx context.Context, ticker string, from, to time.Time) ([]models.CandleDaily, error) {
	if err := p.limiter.Wait(ctx); err != nil {
		return nil, err
	}

	u := p.buildURL(ticker, from, to)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, err
	}

	start := time.Now()
	resp, err := p.client.Do(req)
	if err != nil {
		metrics.MarketDataFetchesTotal.WithLabelValues(p.Name(), "failure").Inc()
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck // body must be closed to avoid resource leak; error is intentionally ignored

	switch resp.StatusCode {
	case http.StatusOK:
		// handled below
	case http.StatusTooManyRequests:
		metrics.MarketDataFetchesTotal.WithLabelValues(p.Name(), "failure").Inc()
		return nil, fmt.Errorf("twelvedata rate limit exceeded (HTTP 429); reduce RequestsPerMin")
	default:
		metrics.MarketDataFetchesTotal.WithLabelValues(p.Name(), "failure").Inc()
		return nil, fmt.Errorf("twelvedata HTTP %d", resp.StatusCode)
	}

	var result twelveDataResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		metrics.MarketDataFetchesTotal.WithLabelValues(p.Name(), "failure").Inc()
		return nil, fmt.Errorf("decode twelvedata response for %s: %w", ticker, err)
	}

	if result.Status != "ok" {
		// TwelveData uses {"status":"error","message":"..."} for errors.
		if result.Message != "" {
			metrics.MarketDataFetchesTotal.WithLabelValues(p.Name(), "failure").Inc()
			return nil, fmt.Errorf("twelvedata error for %s: %s", ticker, result.Message)
		}
		metrics.MarketDataFetchesTotal.WithLabelValues(p.Name(), "failure").Inc()
		return nil, fmt.Errorf("twelvedata unknown status %q for %s", result.Status, ticker)
	}

	metrics.MarketDataFetchesTotal.WithLabelValues(p.Name(), "success").Inc()
	metrics.MarketDataFetchDuration.WithLabelValues(p.Name()).Observe(time.Since(start).Seconds())
	return p.convertValues(ticker, result.Values), nil
}

func (p *TwelveDataProvider) buildURL(ticker string, from, to time.Time) string {
	params := url.Values{}
	params.Set("symbol", ticker)
	params.Set("interval", "1day")
	params.Set("start_date", from.Format("2006-01-02"))
	params.Set("end_date", to.Format("2006-01-02"))
	params.Set("outputsize", "5000")
	params.Set("order", "ASC")
	params.Set("apikey", p.apiKey)
	return twelveDataBaseURL + "/time_series?" + params.Encode()
}

func (p *TwelveDataProvider) convertValues(ticker string, values []twelveDataBar) []models.CandleDaily {
	out := make([]models.CandleDaily, 0, len(values))
	providerName := p.Name()
	for _, v := range values {
		date, err := time.Parse("2006-01-02", v.Datetime)
		if err != nil {
			continue // skip malformed dates
		}
		open, _ := strconv.ParseFloat(v.Open, 64)
		high, _ := strconv.ParseFloat(v.High, 64)
		low, _ := strconv.ParseFloat(v.Low, 64)
		closeVal, _ := strconv.ParseFloat(v.Close, 64)
		volume, _ := strconv.ParseInt(v.Volume, 10, 64)

		out = append(out, models.CandleDaily{
			Ticker:        ticker,
			Date:          date.UTC(),
			Open:          open,
			High:          high,
			Low:           low,
			Close:         closeVal,
			AdjustedClose: nil, // not provided by free-tier TwelveData
			Volume:        volume,
			Provider:      providerName,
		})
	}
	return out
}

// ── JSON response shapes ──────────────────────────────────────────────────────

type twelveDataResponse struct {
	Status  string          `json:"status"`
	Message string          `json:"message"` // populated on error
	Values  []twelveDataBar `json:"values"`
}

type twelveDataBar struct {
	Datetime string `json:"datetime"`
	Open     string `json:"open"`
	High     string `json:"high"`
	Low      string `json:"low"`
	Close    string `json:"close"`
	Volume   string `json:"volume"`
}
