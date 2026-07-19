package models

// EvaluationParsedTicker represents one parsed ticker recommendation from an LLM evaluation.
type EvaluationParsedTicker struct {
	Ticker             string   `json:"ticker"`
	Rank               int      `json:"rank"`
	Setup              string   `json:"setup"`
	Conviction         string   `json:"conviction"`
	EntryLow           *float64 `json:"entry_low"`
	EntryHigh          *float64 `json:"entry_high"`
	StopPrice          *float64 `json:"stop_price"`
	Target1            *float64 `json:"target_1"`
	Target2            *float64 `json:"target_2"`
	RiskPct            *float64 `json:"risk_pct"`
	RiskReward         *float64 `json:"risk_reward"`
	PositionSize       string   `json:"position_size"`
	HoldPeriod         string   `json:"hold_period"`
	AccumDistrib       string   `json:"accum_distrib"`
	Disqualified       bool     `json:"disqualified"`
	DisqualifierReason string   `json:"disqualifier_reason,omitempty"`
}

// EvaluationParsedOutput is the machine-readable extraction from an LLM evaluation's raw response.
type EvaluationParsedOutput struct {
	Tickers       []EvaluationParsedTicker `json:"tickers"`
	ParsedAt      string                   `json:"parsed_at"`
	ParserVersion string                   `json:"parser_version"`
	ParseSuccess  bool                     `json:"parse_success"`
	ParseErrors   []string                 `json:"parse_errors,omitempty"`

	// MLFeatureUpdate is populated from the outcome analyzer prompt's JSON output.
	// It holds an LLM-suggested weight adjustment for a scoring feature.
	MLFeatureUpdate *MLFeatureUpdate `json:"ml_feature_update,omitempty"`
}
