"""Run all 3 screeners and print results."""
import sys, os
sys.path.insert(0, "/app")

from app.config import load_config
from app.clients.tradingview_client import TradingViewClient
from app.screeners import episodic_pivot, momentum, market_leaders

config = load_config()
client = TradingViewClient.from_config(config)

print("=" * 60)
print("EP SCREENER")
print("=" * 60)
ep_df = episodic_pivot.run(client)
print(f"Total: {len(ep_df)} tickers")
if len(ep_df) > 0:
    for _, row in ep_df.iterrows():
        t = row.get("name", "")
        gap = row.get("gap", 0)
        chg = row.get("change", 0)
        rv = row.get("relative_volume_10d_calc", 0)
        print(f"  {t:<8} gap:{gap:>6.1f}%  chg:{chg:>6.1f}%  relVol:{rv:>5.1f}x")
print()

print("=" * 60)
print("MOMENTUM SCREENER")
print("=" * 60)
mom_df = momentum.run(client)
print(f"Total: {len(mom_df)} tickers")
if len(mom_df) > 0:
    for _, row in mom_df.head(60).iterrows():
        t = row.get("name", "")
        p6m = row.get("Perf.6M", 0) or 0
        p3m = row.get("Perf.3M", 0) or 0
        p1m = row.get("Perf.1M", 0) or 0
        rv = row.get("relative_volume_10d_calc", 0) or 0
        print(f"  {t:<8} 6M:{p6m:>7.1f}%  3M:{p3m:>7.1f}%  1M:{p1m:>7.1f}%  rv:{rv:>4.1f}x")
print()

print("=" * 60)
print("LEADERS SCREENER")
print("=" * 60)
lead_df = market_leaders.run(client)
print(f"Total: {len(lead_df)} tickers")
if len(lead_df) > 0:
    for _, row in lead_df.head(60).iterrows():
        t = row.get("name", "")
        eps_g = row.get("earnings_per_share_diluted_yoy_growth_fq", 0) or 0
        roe = row.get("return_on_equity_fq", 0) or 0
        p3m = row.get("Perf.3M", 0) or 0
        print(f"  {t:<8} EPSg:{eps_g:>7.1f}%  ROE:{roe:>6.1f}%  3M:{p3m:>6.1f}%")
