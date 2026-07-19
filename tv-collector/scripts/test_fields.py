from tradingview_screener.query import Query

# All candidate field names to test
candidates = [
    # EPS / earnings
    "earnings_per_share_basic_ttm",
    "earnings_per_share_diluted_ttm",
    "earnings_per_share_forecast_next_fq",
    "eps_surprise_percent",
    "eps_growth_ttm_yoy",
    "EPS_growth_ttm_yoy",
    "earnings_growth_rate_ttm",
    # Revenue
    "revenue_growth_5y",
    "revenue_growth_ttm_yoy",
    "total_revenue_ttm",
    # ROE / margins
    "return_on_equity_fq",
    "roe_ttm",
    "gross_profit_margin_ttm",
    "gross_margin",
    "net_margin",
    "operating_margin",
    # Price / momentum
    "Perf.1M",
    "Perf.3M",
    "Perf.6M",
    "Perf.Y",
    "Perf.YTD",
    "change_from_open",
    "gap",
    # Earnings dates
    "earnings_release_date",
    "next_earnings_date",
    "earnings_release_next_date",
    "earnings_per_share_forecast_fq",
    # Already known good
    "relative_volume_10d_calc",
    "average_volume_10d_calc",
    "market_cap_basic",
    "price_52_week_high",
    "price_52_week_low",
    "RSI",
    "SMA20",
    "SMA50",
    "SMA200",
    "EMA20",
    "ADX",
    "MACD.macd",
    "BB.upper",
    "ATR",
    "sector",
    "industry",
    "exchange",
    "country",
]

ok = []
errors = []

for field in candidates:
    try:
        _, df = Query().select(field).set_markets("america").limit(1).get_scanner_data()
        ok.append(field)
        print(f"OK  {field}")
    except Exception as e:
        msg = str(e)
        if "Unknown field" in msg:
            errors.append(field)
            print(f"BAD {field}")
        else:
            print(f"ERR {field} -> {msg[:60]}")

print("\n=== VALID ===")
for f in ok:
    print(f"  {f}")
print("\n=== INVALID ===")
for f in errors:
    print(f"  {f}")

