package outcomes

import (
	"fmt"
	"strings"
)

// ── R/R Floor Constants ───────────────────────────────────────────────────────
const (
	MinEPRR      = 2.5 // EP Tier 1/2
	MinLeadersRR = 3.0 // Leaders, Momentum, VCP, TIGHT_BASE
)

// Setup type classification helper.
var epSetups = map[string]bool{
	"EP_TIER1": true,
	"EP_TIER2": true,
}

var leadersSetups = map[string]bool{
	"LEADERS":    true,
	"MOMENTUM":   true,
	"VCP":        true,
	"TIGHT_BASE": true,
}

// normalizeSetup normalizes setup type to uppercase for comparison.
func normalizeSetup(setupType string) string {
	return strings.ToUpper(strings.TrimSpace(setupType))
}

// ValidateLevels checks that the stop/entry geometry and R/R floor are valid.
// Returns nil on success, or a descriptive error on any violation.
//
// Rules:
//   - stop must be strictly less than entryLow
//   - t1 must be strictly greater than entryHigh
//   - if t2 > 0, t2 must be strictly greater than t1
//   - R/R floor: EP >= 2.5:1, Leaders/Momentum/VCP/TIGHT_BASE >= 3.0:1
//
// t1 and t2 are *float64 (optional — nil means no target set).
func ValidateLevels(entryLow, entryHigh, stop float64, t1, t2 *float64, setupType string) error {
	setup := normalizeSetup(setupType)

	// Rule 1: stop must be strictly less than entryLow
	if stop >= entryLow {
		return fmt.Errorf("stop (%.2f) >= entry_low (%.2f) for setup %q — stop too high",
			stop, entryLow, setup)
	}

	// Rule 2: t1 must be strictly greater than entryHigh
	if t1 == nil {
		return fmt.Errorf("target_1 is nil for setup %q — no target set", setup)
	}
	if *t1 <= entryHigh {
		return fmt.Errorf("target_1 (%.2f) <= entry_high (%.2f) for setup %q — target too low",
			*t1, entryHigh, setup)
	}

	// Rule 3: if t2 > 0, t2 must be strictly greater than t1
	if t2 != nil && *t2 > 0 && *t2 <= *t1 {
		return fmt.Errorf("target_2 (%.2f) <= target_1 (%.2f) for setup %q — T2 below T1",
			*t2, *t1, setup)
	}

	// Rule 4: R/R floor by setup type
	// R/R = (t1 - entryHigh) / (entryLow - stop)
	risk := entryLow - stop
	if risk <= 0 {
		return fmt.Errorf("risk (entry_low - stop = %.2f) <= 0 for setup %q",
			risk, setup)
	}
	rr := (*t1 - entryHigh) / risk

	if epSetups[setup] {
		if rr < MinEPRR {
			return fmt.Errorf("R/R %.2f < minimum %.1f for EP setup %q",
				rr, MinEPRR, setup)
		}
	} else if leadersSetups[setup] {
		if rr < MinLeadersRR {
			return fmt.Errorf("R/R %.2f < minimum %.1f for setup %q",
				rr, MinLeadersRR, setup)
		}
	}
	// Other setups: no R/R floor applied (pass)

	return nil
}
