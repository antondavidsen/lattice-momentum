// File: internal/jobs/corporate_action_job_test.go
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ai-stock-service/internal/models"
)

// ── Mock Types ─────────────────────────────────────────────────────────────────

type mockCorporateActionStorer struct {
	actions []models.CorporateAction
	err     error
}

func (m *mockCorporateActionStorer) UpsertBatch(_ context.Context, actions []models.CorporateAction) error {
	if m.err != nil {
		return m.err
	}
	m.actions = append(m.actions, actions...)
	return nil
}

// ── Tests ──────────────────────────────────────────────────────────────────────

// TestCorporateActionJob_RunHappyPath validates the full fetch-and-persist cycle
// with a mock Polygon API server and a mock ticker lister.
func TestCorporateActionJob_RunHappyPath(t *testing.T) {
	t.Parallel()

	var requests int
	polyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer test-api-key" {
			t.Errorf("unexpected Authorization header: %s", r.Header.Get("Authorization"))
		}

		ticker := r.URL.Query().Get("ticker")
		var resp polygonSplitsResponse
		switch ticker {
		case "AAPL":
			resp = polygonSplitsResponse{
				Status: "OK",
				Count:  1,
				Results: []struct {
					ExecutionDate string  `json:"execution_date"`
					SplitTo       float64 `json:"split_to"`
					SplitFrom     float64 `json:"split_from"`
					Ticker        string  `json:"ticker"`
				}{
					{ExecutionDate: "2026-05-01", SplitTo: 4.0, SplitFrom: 1.0, Ticker: "AAPL"},
				},
			}
		case "MSFT":
			resp = polygonSplitsResponse{
				Status: "OK",
				Count:  2,
				Results: []struct {
					ExecutionDate string  `json:"execution_date"`
					SplitTo       float64 `json:"split_to"`
					SplitFrom     float64 `json:"split_from"`
					Ticker        string  `json:"ticker"`
				}{
					{ExecutionDate: "2026-06-15", SplitTo: 1.0, SplitFrom: 10.0, Ticker: "MSFT"},
					{ExecutionDate: "2026-07-01", SplitTo: 2.0, SplitFrom: 1.0, Ticker: "MSFT"},
				},
			}
		default:
			resp = polygonSplitsResponse{Status: "OK", Count: 0}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer polyServer.Close()

	job := &CorporateActionJob{
		repo:   &mockCorporateActionStorer{},
		apiKey: "test-api-key",
		client: &http.Client{
			Transport: &testRoundTripper{
				baseURL: polyServer.URL,
			},
		},
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		tickerLister: func(_ context.Context, days int) ([]string, error) {
			return []string{"AAPL", "MSFT"}, nil
		},
	}

	err := job.RunCorporateActionJob(context.Background())
	if err != nil {
		t.Fatalf("RunCorporateActionJob() unexpected error: %v", err)
	}

	if requests != 2 {
		t.Fatalf("expected 2 API requests, got %d", requests)
	}

	storer := job.repo.(*mockCorporateActionStorer)
	if len(storer.actions) != 3 {
		t.Fatalf("expected 3 actions, got %d", len(storer.actions))
	}

	// Verify first action (AAPL split 4:1).
	act := storer.actions[0]
	if act.Ticker != "AAPL" {
		t.Errorf("expected ticker AAPL, got %s", act.Ticker)
	}
	if act.ActionType != "split" {
		t.Errorf("expected action_type 'split', got %s", act.ActionType)
	}
	if act.Ratio != 4.0 {
		t.Errorf("expected ratio 4.0, got %f", act.Ratio)
	}
	if act.Source != "polygon" {
		t.Errorf("expected source 'polygon', got %s", act.Source)
	}

	// Verify second action (MSFT reverse split 1:10).
	act = storer.actions[1]
	if act.Ticker != "MSFT" {
		t.Errorf("expected ticker MSFT, got %s", act.Ticker)
	}
	if act.ActionType != "reverse_split" {
		t.Errorf("expected action_type 'reverse_split', got %s", act.ActionType)
	}
	if act.Ratio != 0.1 {
		t.Errorf("expected ratio 0.1, got %f", act.Ratio)
	}
}

// TestCorporateActionJob_EmptyTickers validates that zero tickers produces zero
// actions without error.
func TestCorporateActionJob_EmptyTickers(t *testing.T) {
	t.Parallel()

	job := &CorporateActionJob{
		repo:   &mockCorporateActionStorer{},
		apiKey: "test",
		client: &http.Client{},
		log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		tickerLister: func(_ context.Context, days int) ([]string, error) {
			return nil, nil
		},
	}

	err := job.RunCorporateActionJob(context.Background())
	if err != nil {
		t.Fatalf("expected nil error for empty tickers, got: %v", err)
	}

	storer := job.repo.(*mockCorporateActionStorer)
	if len(storer.actions) != 0 {
		t.Errorf("expected 0 actions, got %d", len(storer.actions))
	}
}

// TestCorporateActionJob_TickerListerError ensures DB errors propagate.
func TestCorporateActionJob_TickerListerError(t *testing.T) {
	t.Parallel()

	job := &CorporateActionJob{
		repo:   &mockCorporateActionStorer{},
		apiKey: "test",
		client: &http.Client{},
		log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		tickerLister: func(_ context.Context, days int) ([]string, error) {
			return nil, errors.New("db connection failed")
		},
	}

	err := job.RunCorporateActionJob(context.Background())
	if err == nil {
		t.Fatal("expected error from ticker lister, got nil")
	}
}

// TestCorporateActionJob_UpsertBatchError ensures upsert failures abort the job.
func TestCorporateActionJob_UpsertBatchError(t *testing.T) {
	t.Parallel()

	polyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := polygonSplitsResponse{
			Status: "OK",
			Count:  1,
			Results: []struct {
				ExecutionDate string  `json:"execution_date"`
				SplitTo       float64 `json:"split_to"`
				SplitFrom     float64 `json:"split_from"`
				Ticker        string  `json:"ticker"`
			}{
				{ExecutionDate: "2026-05-01", SplitTo: 4.0, SplitFrom: 1.0, Ticker: "AAPL"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer polyServer.Close()

	job := &CorporateActionJob{
		repo:   &mockCorporateActionStorer{err: errors.New("upsert failed")},
		apiKey: "test",
		client: &http.Client{
			Transport: &testRoundTripper{baseURL: polyServer.URL},
		},
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		tickerLister: func(_ context.Context, days int) ([]string, error) {
			return []string{"AAPL"}, nil
		},
	}

	err := job.RunCorporateActionJob(context.Background())
	if err == nil {
		t.Fatal("expected error from upsert, got nil")
	}
}

// TestCorporateActionJob_PolygonAPIError verifies 5xx responses are handled
// as non-fatal (logged, not returned).
func TestCorporateActionJob_PolygonAPIError(t *testing.T) {
	t.Parallel()

	polyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"status":"ERROR"}`))
	}))
	defer polyServer.Close()

	job := &CorporateActionJob{
		repo:   &mockCorporateActionStorer{},
		apiKey: "test",
		client: &http.Client{
			Transport: &testRoundTripper{baseURL: polyServer.URL},
		},
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		tickerLister: func(_ context.Context, days int) ([]string, error) {
			return []string{"AAPL"}, nil
		},
	}

	err := job.RunCorporateActionJob(context.Background())
	if err != nil {
		t.Fatalf("expected nil (non-fatal), got: %v", err)
	}

	storer := job.repo.(*mockCorporateActionStorer)
	if len(storer.actions) != 0 {
		t.Errorf("expected 0 actions (API failed), got %d", len(storer.actions))
	}
}

// TestCorporateActionJob_InvalidDateInResponse ensures bad execution dates are
// logged and skipped gracefully.
func TestCorporateActionJob_InvalidDateInResponse(t *testing.T) {
	t.Parallel()

	polyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := polygonSplitsResponse{
			Status: "OK",
			Count:  2,
			Results: []struct {
				ExecutionDate string  `json:"execution_date"`
				SplitTo       float64 `json:"split_to"`
				SplitFrom     float64 `json:"split_from"`
				Ticker        string  `json:"ticker"`
			}{
				{ExecutionDate: "not-a-date", SplitTo: 4.0, SplitFrom: 1.0, Ticker: "AAPL"},
				{ExecutionDate: "2026-05-01", SplitTo: 4.0, SplitFrom: 1.0, Ticker: "AAPL"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer polyServer.Close()

	job := &CorporateActionJob{
		repo:   &mockCorporateActionStorer{},
		apiKey: "test",
		client: &http.Client{
			Transport: &testRoundTripper{baseURL: polyServer.URL},
		},
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		tickerLister: func(_ context.Context, days int) ([]string, error) {
			return []string{"AAPL"}, nil
		},
	}

	err := job.RunCorporateActionJob(context.Background())
	if err != nil {
		t.Fatalf("expected nil, got: %v", err)
	}

	storer := job.repo.(*mockCorporateActionStorer)
	if len(storer.actions) != 1 {
		t.Errorf("expected 1 valid action (invalid date skipped), got %d", len(storer.actions))
	}
}

// TestCorporateActionJob_FromSourcesConstructor verifies the test constructor
// sets up the job correctly.
func TestCorporateActionJob_FromSourcesConstructor(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	storer := &mockCorporateActionStorer{}
	tickerLister := func(_ context.Context, _ int) ([]string, error) {
		return []string{"AAPL"}, nil
	}

	job := NewCorporateActionJobFromSources(storer, tickerLister, "key", logger)
	if job == nil {
		t.Fatal("NewCorporateActionJobFromSources returned nil")
	}
	if job.repo != storer {
		t.Error("repo not set correctly")
	}
	if job.apiKey != "key" {
		t.Error("apiKey not set correctly")
	}
	if job.client == nil {
		t.Error("client is nil")
	}
	if job.client.Timeout != 30*time.Second {
		t.Errorf("expected timeout 30s, got %v", job.client.Timeout)
	}
}

// testRoundTripper rewrites the request URL to point at the test server.
type testRoundTripper struct {
	baseURL string
}

func (rt *testRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Build a new URL using the test server base but keep the path/query from original.
	u := rt.baseURL + req.URL.RequestURI()
	newReq, err := http.NewRequestWithContext(req.Context(), req.Method, u, req.Body) //nolint:gosec // test helper, URL is controlled by the test
	if err != nil {
		return nil, err
	}
	newReq.Header = req.Header.Clone()
	return http.DefaultTransport.RoundTrip(newReq)
}
