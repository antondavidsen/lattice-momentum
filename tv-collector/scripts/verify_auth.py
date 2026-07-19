"""
scripts/verify_auth.py
───────────────────────
Verifies TradingView authentication status.

Run locally:
    python3 scripts/verify_auth.py

Run inside Docker:
    docker exec momentum_tv_collector python3 scripts/verify_auth.py

Exit codes:
    0 — authenticated (live data)
    1 — unauthenticated (delayed data) or error
"""

import http.cookiejar
import os
import sys
from pathlib import Path

# ── 1. Load cookies ───────────────────────────────────────────────────────────

cookies_file = os.getenv("TV_COOKIES_FILE", "").strip()
cookies_file_explicit = bool(cookies_file)
tv_browser   = os.getenv("TV_BROWSER", "chromium").lower()

jar: http.cookiejar.CookieJar | None = None

# Try file first
if cookies_file:
    p = Path(cookies_file)
    if p.exists():
        try:
            file_jar = http.cookiejar.MozillaCookieJar(str(p))
            file_jar.load(ignore_discard=True, ignore_expires=True)
            jar = file_jar
            print(f"[cookies] Loaded from file: {p.name}")
            print(f"[cookies] Total cookies in file: {sum(1 for _ in jar)}")
        except Exception as e:
            print(f"[cookies] Failed to load file: {e}")
            if cookies_file_explicit:
                print("[cookies] TV_COOKIES_FILE was explicitly set but the file could not be loaded")
                print("[cookies] Refusing to fall back to browser cookies — fix the file or unset TV_COOKIES_FILE")
                sys.exit(1)
    else:
        print(f"[cookies] File not found: {p.name}")
        if cookies_file_explicit:
            print("[cookies] TV_COOKIES_FILE was explicitly set but the file does not exist")
            sys.exit(1)
else:
    print("[cookies] TV_COOKIES_FILE not set")

# Fall back to browser
if jar is None:
    try:
        import rookiepy
        loader = getattr(rookiepy, tv_browser, None)
        if loader:
            raw = loader(["tradingview.com"])
            jar = rookiepy.to_cookiejar(raw)
            print(f"[cookies] Loaded from browser: {tv_browser}")
            print(f"[cookies] Total cookies: {sum(1 for _ in jar)}")
        else:
            print(f"[cookies] Browser '{tv_browser}' not available on this platform")
    except Exception as e:
        print(f"[cookies] Browser load failed: {e}")

if jar is None:
    jar = http.cookiejar.CookieJar()
    print("[cookies] No cookies — will use empty jar")

# ── 2. Check for TradingView auth cookies ─────────────────────────────────────

print("\n[auth] Checking for TradingView session cookies...")

TV_AUTH_COOKIES = {"sessionid", "sessionid_sign", "tv_token", "tv_token_sign"}
found_auth = {}

for cookie in jar:
    if cookie.domain and "tradingview" in cookie.domain:
        if cookie.name in TV_AUTH_COOKIES:
            found_auth[cookie.name] = cookie.value[:12] + "..."  # truncate for safety

if found_auth:
    print(f"[auth] ✅ Auth cookies found: {list(found_auth.keys())}")
    authenticated = True
else:
    print("[auth] ❌ No auth cookies found")
    print("       → Data will be delayed ~15 min")
    print("       → To fix: export cookies from your browser using the")
    print('         "cookies.txt" extension and set TV_COOKIES_FILE')
    authenticated = False

# ── 3. Live query test ────────────────────────────────────────────────────────

print("\n[query] Running test query against TradingView screener...")

try:
    from tradingview_screener.query import Query

    kwargs = {"cookies": jar} if authenticated else {}
    total, df = (
        Query()
        .select("name", "close", "volume", "RSI", "sector")
        .set_markets("america")
        .limit(3)
        .get_scanner_data(**kwargs)
    )

    print(f"[query] ✅ Query succeeded — total_count={total}, rows_returned={len(df)}")
    print()
    print(df.to_string(index=False))

except Exception as e:
    print(f"[query] ❌ Query failed: {e}")
    sys.exit(1)

# ── 4. Summary ────────────────────────────────────────────────────────────────

print()
if authenticated:
    print("=" * 50)
    print("✅  AUTHENTICATED — receiving live (real-time) data")
    print("=" * 50)
    sys.exit(0)
else:
    print("=" * 50)
    print("⚠️   UNAUTHENTICATED — data is delayed ~15 minutes")
    print("    Screeners still work; timing is just off.")
    print("=" * 50)
    sys.exit(1)

