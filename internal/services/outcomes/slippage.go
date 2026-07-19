package outcomes

import (
	"ai-stock-service/internal/models"
)

// SlippageTier defines a cost tier for slippage based on liquidity.
type SlippageTier struct {
	Name         string
	MinPrice     float64
	MinADV       float64 // minimum average dollar volume
	RoundTripBps float64
}

// SlippageTiers is the ordered list of tiers from cheapest to most expensive.
// MatchTier returns the first tier that the ticker qualifies for.
var SlippageTiers = []SlippageTier{
	{Name: "leaders", MinPrice: 20.0, MinADV: 50_000_000, RoundTripBps: 20},
	{Name: "momentum", MinPrice: 5.0, MinADV: 10_000_000, RoundTripBps: 30},
	{Name: "ep_tier1", MinPrice: 5.0, MinADV: 5_000_000, RoundTripBps: 50},
	{Name: "ep_tier2", MinPrice: 2.0, MinADV: 2_000_000, RoundTripBps: 100},
	{Name: "default", MinPrice: 0.0, MinADV: 0, RoundTripBps: 75},
}

// stopSlippageBps maps tier name → extra slippage bps for stop exits.
var stopSlippageBps = map[string]float64{
	"leaders":  15,
	"momentum": 25,
	"ep_tier1": 30,
	"ep_tier2": 40,
	"default":  30,
}

// MatchTier returns the highest-tier (lowest cost) slippage tier for which
// the ticker's price and ADV both meet the minimum thresholds.
// If no tier matches, returns the default tier.
func MatchTier(price, adv float64) SlippageTier {
	for _, t := range SlippageTiers {
		if price >= t.MinPrice && adv >= t.MinADV {
			return t
		}
	}
	return SlippageTiers[len(SlippageTiers)-1] // default
}

// NetReturn computes the net return after slippage costs.
// grossReturn: the gross return as a decimal (e.g. 0.015 = 1.5%)
// tier: the assigned SlippageTier
// exitType: optional exit type string; if "stop", extra stop slippage is applied.
func NetReturn(grossReturn float64, tier SlippageTier, exitType *string) float64 {
	net := grossReturn - (tier.RoundTripBps / 10000)
	if exitType != nil && *exitType == "stop" {
		extra := stopSlippageBps[tier.Name]
		net -= extra / 10000
	}
	return net
}

// CappedPositionSize computes the position size capped at 2% of ADV.
// kellySize: the raw Kelly-derived position size (as a percentage of portfolio, e.g. 5.0 = 5%)
// adv: the ticker's 20-day average dollar volume
// entryPrice: the entry price per share
// portfolioValue: the total portfolio value
//
// Returns the actual position size to use and a boolean indicating whether the cap was applied.
func CappedPositionSize(kellySize, adv, portfolioValue float64) (float64, bool) {
	if kellySize <= 0 || adv <= 0 || portfolioValue <= 0 {
		return kellySize, false
	}
	dollarPosition := portfolioValue * (kellySize / 100)
	maxDollarPosition := adv * 0.02
	if dollarPosition > maxDollarPosition {
		cappedDollar := maxDollarPosition
		cappedSize := (cappedDollar / portfolioValue) * 100
		return cappedSize, true
	}
	return kellySize, false
}

// ApplySlippageModel computes and populates net return fields, slippage tier,
// ADV cap, and stop slippage on a TradeOutcomeDaily row.
func ApplySlippageModel(
	row *models.TradeOutcomeDaily,
	price float64,
	adv float64,
	exitType *string,
	kellySize float64,
	portfolioValue float64,
) error {
	// Determine tier
	tier := MatchTier(price, adv)
	row.SlippageTier = &tier.Name

	// Compute net returns for each gross return field that exists
	if row.Return5D != nil {
		net := NetReturn(*row.Return5D, tier, exitType)
		row.NetReturn5D = &net
	}
	if row.Return10D != nil {
		net := NetReturn(*row.Return10D, tier, exitType)
		row.NetReturn10D = &net
	}
	if row.Return20D != nil {
		net := NetReturn(*row.Return20D, tier, exitType)
		row.NetReturn20D = &net
	}

	// ADV cap
	if kellySize > 0 && adv > 0 && portfolioValue > 0 && row.EntryPrice > 0 {
		cappedSize, wasCapped := CappedPositionSize(kellySize, adv, portfolioValue)
		row.ADVCapApplied = wasCapped
		if wasCapped {
			row.ADVCapPct = &cappedSize
		}
	}

	// Stop slippage
	if exitType != nil && *exitType == "stop" {
		extra := stopSlippageBps[tier.Name]
		row.StopSlippageBps = &extra
	}

	return nil
}
