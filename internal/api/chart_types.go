package api

// ── Chart data types for the trade visualization endpoint ─────────────────────

// ChartResponse is returned by GET /api/v1/reports/{date}/chart/{ticker}.
type ChartResponse struct {
	Ticker       string           `json:"ticker"`
	CompanyName  string           `json:"company_name"`
	BaseType     string           `json:"base_type,omitempty"`      // e.g. "VCP", "Flat Base", "Cup with Handle"
	PatternLabel string           `json:"pattern_label,omitempty"`  // human-readable summary for display
	PivotPrice   *float64         `json:"pivot_price,omitempty"`    // resistance / breakout level
	PctFromPivot *float64         `json:"pct_from_pivot,omitempty"` // last close vs pivot, e.g. -3.2 means 3.2% below
	Candles      []ChartOHLC      `json:"candles"`
	SMA21        []ChartPoint     `json:"sma21,omitempty"`
	SMA50        []ChartPoint     `json:"sma50,omitempty"`
	TradeLevels  *TradeLevels     `json:"trade_levels,omitempty"`
	Markers      []ChartMarker    `json:"markers,omitempty"`
	Trendlines   []ChartTrendline `json:"trendlines,omitempty"`
}

// ChartOHLC is a single OHLCV bar formatted for lightweight-charts.
type ChartOHLC struct {
	Time   string  `json:"time"` // "2026-04-15" — lightweight-charts expects this format
	Open   float64 `json:"open"`
	High   float64 `json:"high"`
	Low    float64 `json:"low"`
	Close  float64 `json:"close"`
	Volume int64   `json:"volume"`
}

// TradeLevels are the horizontal price lines overlaid on the chart.
type TradeLevels struct {
	EntryLow     *float64 `json:"entry_low,omitempty"`
	EntryHigh    *float64 `json:"entry_high,omitempty"`
	StopLoss     *float64 `json:"stop_loss,omitempty"`
	Target1      *float64 `json:"target_1,omitempty"`
	Target2      *float64 `json:"target_2,omitempty"`
	CurrentPrice *float64 `json:"current_price,omitempty"`
}

// ChartMarker is a visual marker (arrow, circle) on a specific date.
type ChartMarker struct {
	Time     string `json:"time"`     // "2026-04-15"
	Position string `json:"position"` // "aboveBar" | "belowBar"
	Color    string `json:"color"`    // hex color
	Shape    string `json:"shape"`    // "arrowUp" | "arrowDown" | "circle"
	Text     string `json:"text"`     // label
}

// ChartPoint is a single time+value point for line series (SMA, etc.).
type ChartPoint struct {
	Time  string  `json:"time"` // "2026-04-15"
	Value float64 `json:"value"`
}

// ChartTrendline is a diagonal or horizontal line connecting two time/price
// points — used for support, resistance, VCP channels, and base boundaries.
type ChartTrendline struct {
	Point1 ChartPoint `json:"p1"`    // start anchor {time, value=price}
	Point2 ChartPoint `json:"p2"`    // end anchor   {time, value=price}
	Role   string     `json:"role"`  // "support" | "resistance"
	Label  string     `json:"label"` // e.g. "VCP resistance", "Base support"
}

// IntradayChartOHLC is a single 5-min OHLCV bar for intraday charts.
// Time is a Unix timestamp (int64) — lightweight-charts v5 accepts this as
// UTCTimestamp when timeScale.timeVisible is true.
type IntradayChartOHLC struct {
	Time   int64   `json:"time"` // Unix seconds — native number for lightweight-charts
	Open   float64 `json:"open"`
	High   float64 `json:"high"`
	Low    float64 `json:"low"`
	Close  float64 `json:"close"`
	Volume int64   `json:"volume"`
}

// IntradayChartPoint is a single time+value point for intraday line series (VWAP, etc.).
type IntradayChartPoint struct {
	Time  int64   `json:"time"` // Unix seconds
	Value float64 `json:"value"`
}
