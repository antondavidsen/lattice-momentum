package models

// SectorContext is the enriched sector view exposed to the commercial report.
// Only leading and lagging sectors are surfaced — not the full universe.
type SectorContext struct {
	ETF           string  `json:"etf"`
	SectorName    string  `json:"sector_name"`
	Description   string  `json:"description"`    // plain-English, 1 sentence
	Perf1M        float64 `json:"perf_1m"`        // % change (21-session)
	Perf3M        float64 `json:"perf_3m"`        // % change (63-session)
	MomentumScore float64 `json:"momentum_score"` // composite 0–1
	RankPosition  int     `json:"rank_position"`  // 1 = best
	Label         string  `json:"label"`          // "Leading" | "Lagging"
}
