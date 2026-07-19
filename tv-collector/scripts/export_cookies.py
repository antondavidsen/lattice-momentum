"""
scripts/export_cookies.py
──────────────────────────
Exports TradingView cookies from the host Chrome browser to
secrets/tradingview_cookies.txt in Netscape format.

Run this on the HOST machine (not inside Docker) whenever cookies expire.

Usage:
    python3 scripts/export_cookies.py           # Chrome (default)
    python3 scripts/export_cookies.py --browser firefox
"""

import argparse
import http.cookiejar
import sys
from pathlib import Path


def main() -> None:
    parser = argparse.ArgumentParser(description="Export TradingView cookies from host browser")
    parser.add_argument("--browser", default="chrome", help="Browser to read cookies from (default: chrome)")
    parser.add_argument("--out", default="secrets/tradingview_cookies.txt", help="Output file path")
    args = parser.parse_args()

    try:
        import rookiepy
    except ImportError:
        print("ERROR: rookiepy not installed. Run: pip install rookiepy")
        sys.exit(1)

    loader = getattr(rookiepy, args.browser.lower(), None)
    if loader is None:
        print(f"ERROR: Browser '{args.browser}' not supported by rookiepy on this platform.")
        print(f"Available: {[x for x in dir(rookiepy) if not x.startswith('_') and callable(getattr(rookiepy, x))]}")
        sys.exit(1)

    print(f"Reading cookies from {args.browser}...")
    try:
        raw = loader(["tradingview.com"])
        src_jar = rookiepy.to_cookiejar(raw)
    except Exception as e:
        print(f"ERROR: Could not read browser cookies: {e}")
        print("Make sure Chrome is installed and you have visited tradingview.com while logged in.")
        sys.exit(1)

    count = sum(1 for _ in src_jar)
    if count == 0:
        print("ERROR: No cookies found for tradingview.com in Chrome.")
        print("→ Open Chrome, go to https://www.tradingview.com, log in, then retry.")
        sys.exit(1)

    # Save to Netscape format
    out = Path(args.out)
    out.parent.mkdir(parents=True, exist_ok=True)

    dest_jar = http.cookiejar.MozillaCookieJar(str(out))
    for c in src_jar:
        dest_jar.set_cookie(c)
    dest_jar.save(ignore_discard=True, ignore_expires=True)

    # Verify auth cookies are present
    auth_cookies = {c.name for c in dest_jar if c.name in {"sessionid", "sessionid_sign"}}
    if "sessionid" not in auth_cookies:
        print(f"WARNING: sessionid not found — you may not be logged in to TradingView in {args.browser}.")

    print(f"✅  Saved {count} cookies → {out}")
    print(f"   Auth cookies: {sorted(auth_cookies)}")
    print()
    print("Next step: docker compose restart tv-collector")


if __name__ == "__main__":
    main()

