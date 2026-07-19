package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// ── PremarketSnapshot ─────────────────────────────────────────────────────────

// PremarketSnapshot represents one row in the premarket_snapshots table.
type PremarketSnapshot struct {
	ID                 uuid.UUID `db:"id"`
	Ticker             string    `db:"ticker"`
	Date               time.Time `db:"date"`
	PrevClose          float64   `db:"prev_close"`
	PremarketPrice     float64   `db:"premarket_price"`
	PremarketHigh      float64   `db:"premarket_high"`
	PremarketLow       float64   `db:"premarket_low"`
	PremarketVolume    int64     `db:"premarket_volume"`
	PremarketVWAP      *float64  `db:"premarket_vwap"`
	GapPct             float64   `db:"gap_pct"`
	RelVolume          float64   `db:"rel_volume"`
	RelVolumeDaily     float64   `db:"rel_volume_daily"`
	DollarVolume       float64   `db:"dollar_volume"`
	FloatShares        *int64    `db:"float_shares"`
	SharesOutstanding  *int64    `db:"shares_outstanding"`
	MarketCap          *float64  `db:"market_cap"`
	AvgVolume20D       *int64    `db:"avg_volume_20d"`
	AvgPremarketVolume *int64    `db:"avg_premarket_volume"`
	SnapshotTime       time.Time `db:"snapshot_time"`
	Sector             *string   `db:"sector"`
	CreatedAt          time.Time `db:"created_at"`
}

// ── PremarketNews ─────────────────────────────────────────────────────────────

// PremarketNews represents one row in the premarket_news table.
type PremarketNews struct {
	ID                 uuid.UUID `db:"id"`
	Ticker             string    `db:"ticker"`
	Date               time.Time `db:"date"`
	Headline           string    `db:"headline"`
	Body               *string   `db:"body"`
	Source             *string   `db:"source"`
	Category           string    `db:"category"`
	CatalystCategory   string    `db:"catalyst_category"`
	CatalystScore      int       `db:"catalyst_score"`
	CatalystConfidence *float64  `db:"catalyst_confidence"`
	PublishedAt        time.Time `db:"published_at"`
	CreatedAt          time.Time `db:"created_at"`
}

// ── PremarketReport ───────────────────────────────────────────────────────────

// PremarketReport represents one row in the premarket_reports table.
type PremarketReport struct {
	ID                     uuid.UUID       `db:"id"`
	ReportDate             time.Time       `db:"report_date"`
	SurgeCandidates        json.RawMessage `db:"surge_candidates"`
	LLMAnalysis            *string         `db:"llm_analysis"`
	LLMProvider            *string         `db:"llm_provider"`
	LLMModel               *string         `db:"llm_model"`
	PromptVersion          *string         `db:"prompt_version"`
	UniverseSize           *int            `db:"universe_size"`
	CatalystFiltered       *int            `db:"catalyst_filtered"`
	Regime                 *string         `db:"regime"`
	SurgeWeights           json.RawMessage `db:"surge_weights"`
	LLMValidationFailed    bool            `db:"llm_validation_failed"`
	CandidatesPreFilter    *int            `db:"candidates_pre_filter"`
	CandidatesPostGap      *int            `db:"candidates_post_gap"`
	CandidatesPostVolume   *int            `db:"candidates_post_volume"`
	CandidatesPostCatalyst *int            `db:"candidates_post_catalyst"`
	CandidatesFinal        *int            `db:"candidates_final"`
	PipelineHealth         *string         `db:"pipeline_health"`
	FollowthroughScores    json.RawMessage `db:"followthrough_scores"`
	CreatedAt              time.Time       `db:"created_at"`
	UpdatedAt              time.Time       `db:"updated_at"`
}

// ── SurgeCandidate (typed JSONB element) ──────────────────────────────────────

// SurgeCandidate represents one candidate in the SurgeCandidates JSONB column of PremarketReport.
type SurgeCandidate struct {
	Rank                 int                `json:"rank"`
	Ticker               string             `json:"ticker"`
	SurgeScore           float64            `json:"surge_score"`
	GapPct               float64            `json:"gap_pct"`
	RelVolume            float64            `json:"rel_volume"`
	DollarVolume         float64            `json:"dollar_volume"`
	CatalystCategory     string             `json:"catalyst_category"`
	CatalystHeadline     string             `json:"catalyst_headline"`
	CatalystScore        int                `json:"catalyst_score"`
	CatalystConfidence   float64            `json:"catalyst_confidence"`
	FloatTurnover        float64            `json:"float_turnover"`
	FloatTurnoverPct     float64            `json:"float_turnover_pct"`
	PriceActionOverride  bool               `json:"price_action_override"`
	Sector               string             `json:"sector"`
	SectorScore          float64            `json:"sector_score"`
	HistoricalSimilarity float64            `json:"historical_similarity"`
	AnalogueQuality      string             `json:"analogue_quality"`
	SimilarRunners       []SimilarRunnerRef `json:"similar_runners"`
	BaseRate             BaseRateRef        `json:"base_rate"`
	LLMConfidence        int                `json:"llm_confidence"`
	LLMThesis            string             `json:"llm_thesis"`
	LLMRiskWarnings      []string           `json:"llm_risk_warnings"`
	SuggestedAllocation  float64            `json:"suggested_allocation_pct"`
	ComponentScores      ComponentScores    `json:"component_scores"`
	RelatedTickers       []string           `json:"related_tickers,omitempty"`
	ChartScore           float64            `json:"chart_score"`
	PremarketHigh        *float64           `json:"premarket_high,omitempty"`
	PremarketLow         *float64           `json:"premarket_low,omitempty"`
	PremarketVWAP        *float64           `json:"premarket_vwap,omitempty"`
	SnapshotTime         *time.Time         `json:"snapshot_time,omitempty"`
	PriorMonthReturn     *float64           `json:"prior_month_return,omitempty"`
}

// ComponentScores holds the individual component scores that contribute to the overall surge score.
type ComponentScores struct {
	Gap        float64 `json:"gap"`
	Volume     float64 `json:"volume"`
	Catalyst   float64 `json:"catalyst"`
	Float      float64 `json:"float"`
	Sector     float64 `json:"sector"`
	Similarity float64 `json:"similarity"`
	Chart      float64 `json:"chart"`
}

// SimilarRunnerRef holds reference information for similar runners.
type SimilarRunnerRef struct {
	Ticker         string  `json:"ticker"`
	Date           string  `json:"date"`
	IntradayReturn float64 `json:"intraday_return"`
	Catalyst       string  `json:"catalyst"`
}

// BaseRateRef holds the historical base rate statistics for a given catalyst category.
type BaseRateRef struct {
	MedianReturn float64 `json:"median_return"`
	WinRate      float64 `json:"win_rate"`
	SampleSize   int     `json:"sample_size"`
}

// ── Followthrough Models ──────────────────────────────────────────────────────

// OpeningRange holds the computed opening range (09:30–09:44 ET) metrics.
type OpeningRange struct {
	High     float64 `json:"or_high"`
	Low      float64 `json:"or_low"`
	RangePct float64 `json:"or_range_pct"`
	Quality  float64 `json:"or_quality"`
}

// VWAPDeviation holds VWAP deviation scores at key intraday checkpoints.
type VWAPDeviation struct {
	At0945 float64 `json:"at_0945"`
	At1015 float64 `json:"at_1015"`
	Score  float64 `json:"vwap_score"`
}

// TickerFollowthrough holds the follow-through quality scores for one ticker.
type TickerFollowthrough struct {
	ORQuality  float64 `json:"or_quality"`
	VWAPScore  float64 `json:"vwap_score"`
	ChartScore float64 `json:"chart_score"`
}

// FollowthroughScores maps ticker → TickerFollowthrough for the JSONB column.
type FollowthroughScores map[string]TickerFollowthrough
