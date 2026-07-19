// Package presentation transforms raw pipeline data into commercial,
// subscriber-friendly language for the Alpha Engine product.
package presentation

import (
	"ai-stock-service/internal/models"
	"fmt"
)

// ── Public response types ─────────────────────────────────────────────────────

// Report is the top-level commercial report delivered to subscribers.
type Report struct {
	Date           string          `json:"date"`
	Title          string          `json:"title"`
	Subtitle       string          `json:"subtitle"`
	MarketRegime   *RegimeCard     `json:"market_regime,omitempty"`
	SectorStrength []SectorCard    `json:"sector_strength,omitempty"`
	TradeIdeas     []TradeIdeaCard `json:"trade_ideas,omitempty"`
	RiskNotes      []string        `json:"risk_notes,omitempty"`
	RawAnalysis    string          `json:"raw_analysis,omitempty"`
	GeneratedBy    string          `json:"generated_by"`
}

// RegimeCard is the market-environment badge.
type RegimeCard struct {
	Label       string  `json:"label"`       // "Bullish", "Bearish", etc.
	Badge       string  `json:"badge"`       // "🟢", "🔴", "🟡"
	Confidence  float64 `json:"confidence"`  // 0–100
	Description string  `json:"description"` // subscriber-friendly copy
}

// SectorCard describes one sector's momentum for the UI.
type SectorCard struct {
	ETF         string  `json:"etf"`
	Label       string  `json:"label"`       // "Leading", "Strong", etc.
	Score       float64 `json:"score"`       // 0–1
	Description string  `json:"description"` // e.g. "Technology is showing strong momentum"
}

// TradeIdeaCard is a single trade idea presented commercially.
type TradeIdeaCard struct {
	Rank        int    `json:"rank"`
	Ticker      string `json:"ticker"`
	ListType    string `json:"list_type"`
	Headline    string `json:"headline"`    // e.g. "NVDA — Breakout setup"
	Description string `json:"description"` // 1–2 sentence plain-English explanation
}

// ── Trade outcome types ───────────────────────────────────────────────────────

// OutcomeCard is a past trade result presented in marketing-friendly language.
type OutcomeCard struct {
	Ticker          string   `json:"ticker"`
	EntryDate       string   `json:"entry_date"`
	ListType        string   `json:"list_type"`
	EntryPrice      float64  `json:"entry_price"`
	DaysTracked     int      `json:"days_tracked"`
	WeeklyReturn    string   `json:"weekly_return,omitempty"` // "+2.3% after first week"
	MaxProfit       string   `json:"max_profit,omitempty"`    // "Reached +8.1% potential profit"
	MaxDrawdown     string   `json:"max_drawdown,omitempty"`  // "Controlled downside: -1.9%"
	StatusBadge     string   `json:"status_badge"`            // "✅ Winner", "⚠️ Flat", "❌ Stopped"
	WeeklyReturnPct *float64 `json:"weekly_return_pct,omitempty"`
	MaxProfitPct    *float64 `json:"max_profit_pct,omitempty"`
	MaxDrawdownPct  *float64 `json:"max_drawdown_pct,omitempty"`

	// ── V5 enrichment fields ──────────────────────────────────────────────────
	ExitType             *string  `json:"exit_type,omitempty"`
	NetReturn5D          *float64 `json:"net_return_5d,omitempty"`
	NetReturn10D         *float64 `json:"net_return_10d,omitempty"`
	NetReturn20D         *float64 `json:"net_return_20d,omitempty"`
	IsPrimaryObservation bool     `json:"is_primary_observation"`
	CrossListDuplicate   bool     `json:"cross_list_duplicate"`
	CorporateActionCount int      `json:"corporate_action_count,omitempty"`
	LevelsInvalid        bool     `json:"levels_invalid"`
}

// PerformanceSummary is the aggregated stats for marketing pages.
// Legacy v4 struct — built from raw TradeOutcomeDaily rows.
type PerformanceSummary struct {
	TotalTrades    int     `json:"total_trades"`
	WinRate5D      string  `json:"win_rate_5d"`      // "68%"
	AvgReturn5D    string  `json:"avg_return_5d"`    // "+1.8%"
	AvgMaxProfit   string  `json:"avg_max_profit"`   // "+6.2% average peak profit"
	AvgMaxDrawdown string  `json:"avg_max_drawdown"` // "-2.1% average max drawdown"
	BestTrade      string  `json:"best_trade"`       // "NVDA: +12.4% in 5 days"
	RecentWinRate  string  `json:"recent_win_rate"`  // "Last 30 trades: 72% winners"
	WinRate5DPct   float64 `json:"win_rate_5d_pct"`
	AvgReturn5DPct float64 `json:"avg_return_5d_pct"`

	// Clean numeric fields for frontend (no string parsing needed)
	AvgMaxProfitPct   *float64 `json:"avg_max_profit_pct,omitempty"`
	AvgMaxDrawdownPct *float64 `json:"avg_max_drawdown_pct,omitempty"`

	// Expectancy & per-trade stats
	BaselineWinRate float64 `json:"baseline_win_rate,omitempty"` // default 52.0
	Expectancy      float64 `json:"expectancy,omitempty"`        // (win% * avgWin) - (loss% * avgLoss)
	AvgWin          float64 `json:"avg_win,omitempty"`           // average return of winning trades
	AvgLoss         float64 `json:"avg_loss,omitempty"`          // average return of losing trades (negative)
}

// PerformanceSummaryV5 is the v5 aggregated stats built from the performance_windows table.
// This struct matches the field names expected by PerformanceSection.jsx.
type PerformanceSummaryV5 struct {
	NetReturn20d            *float64 `json:"net_return_20d,omitempty"`
	WinRate                 *float64 `json:"win_rate,omitempty"`
	BaselineWinRate         *float64 `json:"baseline_win_rate,omitempty"`
	DeltaPP                 *float64 `json:"delta_pp,omitempty"`
	Expectancy              *float64 `json:"expectancy,omitempty"`
	AvgWin                  *float64 `json:"avg_win,omitempty"`
	AvgLoss                 *float64 `json:"avg_loss,omitempty"`
	AvgMaxDrawdownPct       *float64 `json:"avg_max_drawdown_pct,omitempty"`
	AvgMaxProfitPct         *float64 `json:"avg_max_profit_pct,omitempty"`
	MaxDrawdown             *float64 `json:"max_drawdown,omitempty"`
	AlertTriggered          bool     `json:"alert_triggered"`
	ConsecutiveDecayWindows int      `json:"consecutive_decay_windows"`
	RegimeLabel             *string  `json:"regime_label,omitempty"`
	RegimeBucket            *string  `json:"regime_bucket,omitempty"`
	EffectiveN              *int     `json:"effective_n,omitempty"`
	RawN                    *int     `json:"raw_n,omitempty"`
	LevelsInvalid           bool     `json:"levels_invalid"`
	WindowDate              *string  `json:"window_date,omitempty"`
	PipelineType            string   `json:"pipeline_type"`
}

// ── Regime formatting ─────────────────────────────────────────────────────────

func regimePresentation(regime string, confidence float64) (label, badge, description string) {
	switch regime {
	case "strong_bull":
		return "Strong Bull Market", "🟢",
			fmt.Sprintf("Markets are in a confirmed uptrend with strong breadth. Confidence: %.0f%%. Favor aggressive swing entries.", confidence)
	case "bull":
		return "Bull Market", "🟢",
			fmt.Sprintf("Overall bullish conditions support momentum trading. Confidence: %.0f%%. Standard position sizing applies.", confidence)
	case "neutral":
		return "Neutral / Transitional", "🟡",
			fmt.Sprintf("Mixed signals — market direction is unclear. Confidence: %.0f%%. Reduce position sizes and be selective.", confidence)
	case "correction":
		return "Market Correction", "🟠",
			fmt.Sprintf("Markets are pulling back. Confidence: %.0f%%. Defensive stance recommended — focus on strongest names only.", confidence)
	case "bear":
		return "Bear Market", "🔴",
			fmt.Sprintf("Broad market weakness detected. Confidence: %.0f%%. Cash is a position. Only trade the highest-conviction setups.", confidence)
	default:
		return "Unknown", "⚪", "Market regime data unavailable."
	}
}

func listTypeFriendly(lt models.ListType) string {
	switch lt {
	case models.ListTypeEP:
		return "catalyst-driven earnings pivot"
	case models.ListTypeMomentum:
		return "momentum breakout"
	case models.ListTypeLeaders:
		return "institutional quality leaders"
	default:
		return string(lt)
	}
}
