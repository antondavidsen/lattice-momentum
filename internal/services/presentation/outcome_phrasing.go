package presentation

import (
	"fmt"

	"ai-stock-service/internal/models"
)

// ── Quant → Marketing Language Mapping ────────────────────────────────────────
//
// These functions transform raw trade outcome metrics into subscriber-friendly
// prose. The goal: make every metric feel like insight from a human analyst,
// not a quant spreadsheet.

// PhraseReturn5D converts a 5-day return to subscriber-friendly language.
func PhraseReturn5D(r *float64) string {
	if r == nil {
		return "First-week performance is still being tracked."
	}
	pct := *r * 100
	switch {
	case pct >= 5:
		return fmt.Sprintf("Exceptional start — this trade surged +%.1f%% in its first week.", pct)
	case pct >= 3:
		return fmt.Sprintf("Strong start — this trade gained +%.1f%% in its first week.", pct)
	case pct >= 0:
		return fmt.Sprintf("This trade gained +%.1f%% in its first week of trading.", pct)
	case pct >= -3:
		return fmt.Sprintf("This trade pulled back %.1f%% in the first week — within normal range.", pct)
	default:
		return fmt.Sprintf("This trade dropped %.1f%% in the first week — stop discipline was key.", pct)
	}
}

// PhraseReturn10D converts a 10-day return to marketing copy.
func PhraseReturn10D(r *float64) string {
	if r == nil {
		return ""
	}
	pct := *r * 100
	if pct >= 0 {
		return fmt.Sprintf("After two weeks, this position was up +%.1f%% from entry.", pct)
	}
	return fmt.Sprintf("After two weeks, this position was down %.1f%% from entry.", pct)
}

// PhraseReturn20D converts a 20-day return to marketing copy.
func PhraseReturn20D(r *float64) string {
	if r == nil {
		return ""
	}
	pct := *r * 100
	if pct >= 0 {
		return fmt.Sprintf("Over the full tracking period, this trade delivered +%.1f%% total return.", pct)
	}
	return fmt.Sprintf("Over the full tracking period, this trade closed at %.1f%% from entry.", pct)
}

// PhraseMaxRunup converts max run-up to marketing copy.
func PhraseMaxRunup(r *float64) string {
	if r == nil {
		return ""
	}
	pct := *r * 100
	switch {
	case pct >= 10:
		return fmt.Sprintf("This trade reached up to +%.1f%% profit — an outstanding move.", pct)
	case pct >= 5:
		return fmt.Sprintf("This trade reached up to +%.1f%% potential profit before pulling back.", pct)
	case pct >= 0:
		return fmt.Sprintf("This trade reached +%.1f%% at its peak.", pct)
	default:
		return ""
	}
}

// PhraseMaxDrawdown converts max drawdown to subscriber-safe language.
func PhraseMaxDrawdown(d *float64) string {
	if d == nil {
		return ""
	}
	pct := *d * 100 // already negative
	switch {
	case pct >= -2:
		return fmt.Sprintf("Minimal drawdown of %.1f%% — an exceptionally clean trade.", pct)
	case pct >= -5:
		return fmt.Sprintf("The worst drawdown was a controlled %.1f%% — well within risk limits.", pct)
	default:
		return fmt.Sprintf("This trade saw a %.1f%% drawdown — stop loss discipline was critical.", pct)
	}
}

// PhraseHoldingPeriod describes the tracking status.
func PhraseHoldingPeriod(evaluatedDays int) string {
	switch {
	case evaluatedDays >= 20:
		return "Fully tracked over 20 trading days."
	case evaluatedDays >= 10:
		return fmt.Sprintf("%d days tracked — entering the extended window.", evaluatedDays)
	default:
		return fmt.Sprintf("%d days into the tracking window — still in progress.", evaluatedDays)
	}
}

// PhraseOutcomeBadge returns a marketing-friendly outcome label with emoji.
func PhraseOutcomeBadge(r5d *float64) string {
	if r5d == nil {
		return "⏳ In Progress"
	}
	r := *r5d
	switch {
	case r >= 0.05:
		return "🏆 Big Winner"
	case r >= 0.03:
		return "✅ Strong Winner"
	case r >= 0.01:
		return "✅ Winner"
	case r >= -0.01:
		return "➖ Flat"
	case r >= -0.03:
		return "⚠️ Small Loss"
	default:
		return "❌ Stopped Out"
	}
}

// ── Enriched Outcome Card Builder ─────────────────────────────────────────────

// EnrichedOutcomeCard is a past trade result presented with full marketing phrasing.
type EnrichedOutcomeCard struct {
	Ticker         string   `json:"ticker"`
	CompanyName    string   `json:"company_name,omitempty"`
	EntryDate      string   `json:"entry_date"`
	StrategyType   string   `json:"strategy_type"`
	EntryPrice     float64  `json:"entry_price"`
	FirstWeek      string   `json:"first_week"`
	TwoWeeks       string   `json:"two_weeks,omitempty"`
	FullPeriod     string   `json:"full_period,omitempty"`
	PeakProfit     string   `json:"peak_profit"`
	WorstDrawdown  string   `json:"worst_drawdown"`
	TrackingStatus string   `json:"tracking_status"`
	OutcomeBadge   string   `json:"outcome_badge"`
	Return5DPct    *float64 `json:"return_5d_pct,omitempty"`
	MaxRunupPct    *float64 `json:"max_runup_pct,omitempty"`
	MaxDrawdownPct *float64 `json:"max_drawdown_pct,omitempty"`
}

// FormatEnrichedOutcome transforms a raw TradeOutcomeDaily into a fully phrased
// marketing card. Use this for the enriched report (Part 4 of the design).
func FormatEnrichedOutcome(o *models.TradeOutcomeDaily, companyName string) EnrichedOutcomeCard {
	card := EnrichedOutcomeCard{
		Ticker:         o.Ticker,
		CompanyName:    companyName,
		EntryDate:      o.EntryDate.Format("2006-01-02"),
		StrategyType:   listTypeFriendly(o.ListType),
		EntryPrice:     o.EntryPrice,
		FirstWeek:      PhraseReturn5D(o.Return5D),
		TwoWeeks:       PhraseReturn10D(o.Return10D),
		FullPeriod:     PhraseReturn20D(o.Return20D),
		PeakProfit:     PhraseMaxRunup(o.MaxRunup20D),
		WorstDrawdown:  PhraseMaxDrawdown(o.MaxDrawdown20D),
		TrackingStatus: PhraseHoldingPeriod(o.EvaluatedDays),
		OutcomeBadge:   PhraseOutcomeBadge(o.Return5D),
	}

	if o.Return5D != nil {
		pct := *o.Return5D * 100
		card.Return5DPct = &pct
	}
	if o.MaxRunup20D != nil {
		pct := *o.MaxRunup20D * 100
		card.MaxRunupPct = &pct
	}
	if o.MaxDrawdown20D != nil {
		pct := *o.MaxDrawdown20D * 100
		card.MaxDrawdownPct = &pct
	}

	return card
}
