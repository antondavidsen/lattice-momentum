// Package ingestion provides services that ingest data from external sources
// into the system.
package ingestion

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// DefaultTVCollectorTimeout is the maximum time the nightly pipeline will
// wait for the tv-collector /run endpoint to respond.
// TradingView browser-based scraping is slow; 10 minutes is generous but safe.
const DefaultTVCollectorTimeout = 10 * time.Minute

// TVCollectorClient triggers the tv-collector Python service to run an
// on-demand TradingView screener collection.
//
// The tv-collector exposes a synchronous POST /run endpoint: the HTTP call
// blocks until all three screeners have been fetched, saved, and POSTed to
// the Go API.  This makes pipeline orchestration simple — wait for 200, then
// proceed to the next step.
type TVCollectorClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewTVCollectorClient constructs a TVCollectorClient.
//
// timeout governs how long the Go pipeline will wait for the collection to
// finish.  Pass 0 to use DefaultTVCollectorTimeout.
func NewTVCollectorClient(baseURL string, timeout time.Duration) *TVCollectorClient {
	if timeout <= 0 {
		timeout = DefaultTVCollectorTimeout
	}
	return &TVCollectorClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// TriggerCollection calls POST /run on the tv-collector service and blocks
// until the collection completes or ctx is cancelled.
//
// Returns nil on HTTP 2xx.
// Returns an error on network failure, HTTP 4xx/5xx, or context cancellation.
// HTTP 409 means a run is already in progress; the pipeline treats this as a
// non-fatal warning and proceeds (the in-flight run will complete shortly).
func (c *TVCollectorClient) TriggerCollection(ctx context.Context) error {
	url := c.baseURL + "/run"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, http.NoBody)
	if err != nil {
		return fmt.Errorf("build tv-collector request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close() //nolint:errcheck // body must be closed to avoid resource leak; error is intentionally ignored

	// 409 = already running — treat as success; the ongoing run will populate
	// today's snapshot data before Step 2 (fundamentals) needs it.
	if resp.StatusCode == http.StatusConflict {
		slog.Warn("tv-collector: run already in progress — treating as success",
			"component", "tv_collector_client",
			"status_code", resp.StatusCode,
			"url", url,
		)
		return nil
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode >= 500 {
			slog.Error("tv-collector: server error",
				"component", "tv_collector_client",
				"status_code", resp.StatusCode,
				"url", url,
			)
		} else {
			slog.Warn("tv-collector: unexpected response",
				"component", "tv_collector_client",
				"status_code", resp.StatusCode,
				"url", url,
			)
		}
		return fmt.Errorf("tv-collector returned HTTP %d for POST %s", resp.StatusCode, url)
	}

	return nil
}
