package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"ai-stock-service/internal/models"
	"ai-stock-service/internal/repository"
)

// polygonSplitsResponse mirrors the Polygon /v3/reference/splits response.
type polygonSplitsResponse struct {
	Results []struct {
		ExecutionDate string  `json:"execution_date"`
		SplitTo       float64 `json:"split_to"`
		SplitFrom     float64 `json:"split_from"`
		Ticker        string  `json:"ticker"`
	} `json:"results"`
	Status  string `json:"status"`
	Count   int    `json:"count"`
	NextURL string `json:"next_url"`
}

// corporateActionStorer is the subset of repo the job needs.
type corporateActionStorer interface {
	UpsertBatch(ctx context.Context, actions []models.CorporateAction) error
}

// Compile-time assertions.
var _ corporateActionStorer = (*repository.CorporateActionRepo)(nil)

// CorporateActionJob fetches corporate actions (splits/reverse splits) from the
// Polygon reference API for all active tickers and persists them to the
// corporate_actions table. Non-fatal: failures are logged but never abort
// the pipeline.
type CorporateActionJob struct {
	repo         corporateActionStorer
	tickerLister func(ctx context.Context, days int) ([]string, error)
	apiKey       string
	client       *http.Client
	log          *slog.Logger
}

// NewCorporateActionJob constructs a CorporateActionJob from production types.
func NewCorporateActionJob(
	repo *repository.CorporateActionRepo,
	apiKey string,
	log *slog.Logger,
) *CorporateActionJob {
	return &CorporateActionJob{
		repo:   repo,
		apiKey: apiKey,
		client: &http.Client{Timeout: 30 * time.Second},
		log:    log,
		tickerLister: func(ctx context.Context, days int) ([]string, error) {
			return listDistinctTickers(ctx, repo, days)
		},
	}
}

// NewCorporateActionJobFromSources constructs a CorporateActionJob from interfaces.
// Intended for tests.
func NewCorporateActionJobFromSources(
	storer corporateActionStorer,
	tickerLister func(ctx context.Context, days int) ([]string, error),
	apiKey string,
	log *slog.Logger,
) *CorporateActionJob {
	return &CorporateActionJob{
		repo:         storer,
		tickerLister: tickerLister,
		apiKey:       apiKey,
		client:       &http.Client{Timeout: 30 * time.Second},
		log:          log,
	}
}

// RunCorporateActionJob fetches corporate actions for all active tickers and
// persists them. Non-fatal: returns nil on API errors (logged as warnings).
func (j *CorporateActionJob) RunCorporateActionJob(ctx context.Context) error {
	start := time.Now()

	j.log.Info("job starting",
		"job", "CorporateActionJob",
	)

	// 1. Fetch distinct tickers from daily_rank_lists (last 30 days).
	tickers, err := j.tickerLister(ctx, 30)
	if err != nil {
		return fmt.Errorf("CorporateActionJob: list tickers: %w", err)
	}

	if len(tickers) == 0 {
		j.log.Info("job completed — no tickers found",
			"job", "CorporateActionJob",
			"duration_ms", time.Since(start).Milliseconds(),
		)
		return nil
	}

	j.log.Info("tickers loaded",
		"job", "CorporateActionJob",
		"tickers_scanned", len(tickers),
	)

	// 2. Fetch splits for each ticker.
	var allActions []models.CorporateAction
	var failed int
	for _, ticker := range tickers {
		actions, err := j.fetchSplits(ctx, ticker)
		if err != nil {
			j.log.Warn("CorporateActionJob: fetch failed for ticker, skipping",
				"ticker", ticker,
				"error", err,
			)
			failed++
			continue
		}
		allActions = append(allActions, actions...)
	}

	// 3. Batch upsert.
	if len(allActions) > 0 {
		if err := j.repo.UpsertBatch(ctx, allActions); err != nil {
			return fmt.Errorf("CorporateActionJob: upsert batch: %w", err)
		}
	}

	// 4. Summary.
	j.log.Info("job completed",
		"job", "CorporateActionJob",
		"duration_ms", time.Since(start).Milliseconds(),
		"tickers_scanned", len(tickers),
		"actions_found", len(allActions),
		"actions_upserted", len(allActions),
		"tickers_failed", failed,
	)

	return nil
}

// fetchSplits queries the Polygon reference API for a single ticker's splits.
func (j *CorporateActionJob) fetchSplits(ctx context.Context, ticker string) ([]models.CorporateAction, error) {
	url := fmt.Sprintf("https://api.polygon.io/v3/reference/splits?ticker=%s&limit=100", ticker)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+j.apiKey)

	resp, err := j.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("polygon API error: status=%d body=%s", resp.StatusCode, string(body))
	}

	var polyResp polygonSplitsResponse
	if err := json.NewDecoder(resp.Body).Decode(&polyResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	var actions []models.CorporateAction
	for _, r := range polyResp.Results {
		exDate, err := time.Parse("2006-01-02", r.ExecutionDate)
		if err != nil {
			j.log.Warn("CorporateActionJob: invalid execution_date, skipping",
				"ticker", ticker,
				"execution_date", r.ExecutionDate,
			)
			continue
		}

		actionType := "split"
		if r.SplitFrom > r.SplitTo {
			actionType = "reverse_split"
		}
		ratio := r.SplitTo / r.SplitFrom

		actions = append(actions, models.CorporateAction{
			Ticker:     ticker,
			ExDate:     exDate,
			ActionType: actionType,
			Ratio:      ratio,
			Source:     "polygon",
		})
	}

	return actions, nil
}

// listDistinctTickers queries daily_rank_lists for distinct tickers
// with entries in the last N days.
func listDistinctTickers(ctx context.Context, repo *repository.CorporateActionRepo, days int) ([]string, error) {
	pool := repo.Pool()
	rows, err := pool.Query(ctx, `SELECT DISTINCT ticker FROM daily_rank_lists WHERE date >= $1`, time.Now().AddDate(0, 0, -days))
	if err != nil {
		return nil, fmt.Errorf("listDistinctTickers: %w", err)
	}
	defer rows.Close()

	var tickers []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, fmt.Errorf("listDistinctTickers scan: %w", err)
		}
		tickers = append(tickers, t)
	}
	return tickers, nil
}
