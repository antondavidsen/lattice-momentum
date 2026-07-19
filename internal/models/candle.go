// Package models contains shared data structures and constants used across the application.
package models

// Benchmarks tracked for regime detection.
// SPY = S&P 500, QQQ = Nasdaq-100, IWM = Russell 2000 (small-cap breadth),
// DIA = Dow Jones Industrial Average.
// VIX = CBOE Volatility Index (daily close used for fear/greed scoring).
// ES1!, NQ1!, RTY1! = index futures (E-mini S&P 500, Nasdaq-100, Russell 2000)
//
//	used for pre-market intraday signals. These require the Polygon INDICES
//	namespace prefix (I:ES1!, I:NQ1!, I:RTY1!) in API calls.
//
// $TICK is an index (not a tradeable ticker) and is handled separately via
// the Polygon indices feed — it does not appear in this list.
var Benchmarks = []string{"SPY", "QQQ", "IWM", "DIA", "VIX", "ES1!", "NQ1!", "RTY1!"}

// SectorETFs tracked for rotation scoring.
// Spec-required set: XLK XLF XLE XLV XLI XLY XLP XLU XLB SMH IGV.
// XLRE (Real Estate) is included as an optional extra; it is not in the original spec.
var SectorETFs = []string{
	"XLK",  // Technology
	"XLV",  // Healthcare
	"XLF",  // Financials
	"XLY",  // Consumer Discretionary
	"XLI",  // Industrials
	"XLE",  // Energy
	"XLB",  // Materials
	"XLU",  // Utilities
	"XLP",  // Consumer Staples
	"XLRE", // Real Estate (bonus — not in original spec)
	"XLC",  // Communication Services
	"SMH",  // Semiconductors (VanEck — required by spec)
	"IGV",  // Software (iShares — required by spec)
}

// criticalTickerSet is the pre-built lookup map for IsCriticalTicker.
// It is constructed once at init time from Benchmarks + SectorETFs.
var criticalTickerSet = func() map[string]struct{} {
	all := make([]string, 0, len(Benchmarks)+len(SectorETFs))
	all = append(all, Benchmarks...)
	all = append(all, SectorETFs...)
	m := make(map[string]struct{}, len(all))
	for _, t := range all {
		m[t] = struct{}{}
	}
	return m
}()

// IsCriticalTicker reports whether ticker is in the CRITICAL ingestion tier.
// CRITICAL tickers (benchmark indices + sector ETFs) must have fresh candles
// before the nightly regime pipeline is allowed to run.
func IsCriticalTicker(ticker string) bool {
	_, ok := criticalTickerSet[ticker]
	return ok
}

// sectorETFByNameMap is the reverse lookup from sector name → ETF ticker.
// Built from the canonical sectorETFMap in internal/services/ranking/shared.go.
var sectorETFByNameMap = func() map[string]string {
	return map[string]string{
		"technology":             "XLK",
		"healthcare":             "XLV",
		"financial services":     "XLF",
		"financials":             "XLF",
		"consumer cyclical":      "XLY",
		"consumer discretionary": "XLY",
		"industrials":            "XLI",
		"energy":                 "XLE",
		"basic materials":        "XLB",
		"materials":              "XLB",
		"utilities":              "XLU",
		"consumer defensive":     "XLP",
		"consumer staples":       "XLP",
		"real estate":            "XLRE",
		"communication services": "XLC",
	}
}()

// SectorETFByName returns the sector ETF ticker for a given sector name.
// The lookup is case-insensitive. Returns ("", false) if the sector is unknown.
func SectorETFByName(name string) (string, bool) {
	etf, ok := sectorETFByNameMap[name]
	return etf, ok
}
