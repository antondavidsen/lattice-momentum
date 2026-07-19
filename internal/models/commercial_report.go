package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// CommercialReport is one row in the commercial_reports table.
// It stores the LLM-transformed subscriber-friendly report for a single day,
// produced by the Commercial Report transformer (Ticket 6.3.1).
type CommercialReport struct {
	ID                 uuid.UUID       `db:"id"`
	ReportDate         time.Time       `db:"report_date"`
	Regime             string          `db:"regime"`
	Headline           string          `db:"headline"`
	MarketSummary      string          `db:"market_summary"`
	SectorSummary      string          `db:"sector_summary"`
	TradeCardsJSON     json.RawMessage `db:"trade_cards_json"`
	RiskNote           string          `db:"risk_note"`
	ClosingSummary     string          `db:"closing_summary"`
	FullReportMarkdown string          `db:"full_report_markdown"`
	PerformanceBlurb   string          `db:"performance_blurb"`
	SourceListTypes    []string        `db:"source_list_types"`
	Provider           string          `db:"provider"`
	Model              string          `db:"model"`
	PromptVersion      string          `db:"prompt_version"`
	InputTokens        *int            `db:"input_tokens"`
	OutputTokens       *int            `db:"output_tokens"`
	DurationMs         *int            `db:"duration_ms"`
	GateLevel          string          `db:"gate_level"`   // R-09: "full" | "ep_only" | "halt"
	RegimeLabel        string          `db:"regime_label"` // R-09: regime label at time of halt
	VIXLevel           float64         `db:"vix_level"`    // R-09: VIX level at time of halt
	CreatedAt          time.Time       `db:"created_at"`
	UpdatedAt          time.Time       `db:"updated_at"`
}

// CommercialTradeCard is the structured trade card produced by the LLM
// transformer and stored in trade_cards_json. This is what the UI renders.
type CommercialTradeCard struct {
	Ticker               string `json:"ticker"`
	CompanyName          string `json:"company_name"`
	Conviction           string `json:"conviction"` // "High" | "Medium" | "Starter"
	StrategyType         string `json:"strategy_type"`
	WhyItMadeTheList     string `json:"why_it_made_the_list"`
	EntryZone            string `json:"entry_zone"`
	StopLoss             string `json:"stop_loss"`
	Target1              string `json:"target_1"`
	Target2              string `json:"target_2"`
	HoldPeriod           string `json:"hold_period"`
	PositionSizeGuidance string `json:"position_size_guidance"`
	EarningsNote         string `json:"earnings_note"`

	// ── Price Structure Assessment ───────────────────────
	BaseType           string `json:"base_type,omitempty"`
	BaseDepth          string `json:"base_depth,omitempty"`
	VolumeBehavior     string `json:"volume_behavior,omitempty"`
	PivotPrice         string `json:"pivot_price,omitempty"`
	ExtensionFromPivot string `json:"extension_from_pivot,omitempty"`

	// ── Institutional Footprint ──────────────────────────
	RelativeVolume        float64 `json:"relative_volume,omitempty"`
	Near52WHighPct        string  `json:"near_52w_high_pct,omitempty"`
	Perf3M6M              string  `json:"perf_3m_6m,omitempty"`
	InstitutionalInterest string  `json:"institutional_interest,omitempty"`

	// ── Risk / Reward & Sizing ───────────────────────────
	RiskRewardRatio string `json:"risk_reward_ratio,omitempty"`
	PositionPct     string `json:"position_pct,omitempty"`

	// ── Trade Outcome Tracking (from trade_outcomes_daily) ─────
	ListType       string   `json:"list_type,omitempty"`
	EntryDate      string   `json:"entry_date,omitempty"`
	EntryPrice     float64  `json:"entry_price,omitempty"`
	Return1D       *float64 `json:"return_1d,omitempty"`
	Return2D       *float64 `json:"return_2d,omitempty"`
	Return3D       *float64 `json:"return_3d,omitempty"`
	Return4D       *float64 `json:"return_4d,omitempty"`
	Return5D       *float64 `json:"return_5d,omitempty"`
	Return10D      *float64 `json:"return_10d,omitempty"`
	Return20D      *float64 `json:"return_20d,omitempty"`
	MaxRunup20D    *float64 `json:"max_runup_20d,omitempty"`
	MaxDrawdown20D *float64 `json:"max_drawdown_20d,omitempty"`
	EvaluatedDays  int      `json:"evaluated_days,omitempty"`

	// ── Computed Fields (populated in presentation layer) ──
	// CurrentReturn is the most recent non-null return available.
	CurrentReturn *float64 `json:"current_return,omitempty"`
	// CurrentDay is which day CurrentReturn represents (1-20). 0 if no data.
	CurrentDay int `json:"current_day,omitempty"`

	// Model score from nightly pipeline (0-10 scale)
	ModelScore *float64 `json:"model_score,omitempty"`
	// Base rate win percentage for this base type
	BaseRateWinPct  float64 `json:"base_rate_win_pct,omitempty"`
	BaseRateSampleN int     `json:"base_rate_sample_n,omitempty"`
	// Duplicate signal tracking
	IsDuplicateSignal     bool `json:"is_duplicate_signal,omitempty"`
	TradingDaysSincePrior int  `json:"trading_days_since_prior,omitempty"`

	// Pipeline type for visual distinction
	PipelineType string `json:"pipeline_type,omitempty"`

	// ── Enrichment Fields (from prompt_ticker_outcomes) ──────────────
	Disqualified       bool    `json:"disqualified,omitempty"`
	DisqualifierReason string  `json:"disqualifier_reason,omitempty"`
	OptionsFlowScore   float64 `json:"options_flow_score,omitempty"`
	NarrativeVelocity  float64 `json:"narrative_velocity,omitempty"`
}

// TradeOfDay is the single highest-conviction trade card selected from
// all three lists combined. Populated in the presentation layer.
type TradeOfDay struct {
	TradeCard       *CommercialTradeCard `json:"trade_card,omitempty"`
	SelectionReason string               `json:"selection_reason,omitempty"`
}
