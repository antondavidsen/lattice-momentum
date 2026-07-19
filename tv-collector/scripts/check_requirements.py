"""
scripts/check_requirements.py
──────────────────────────────
Verifies all prerequisites for real-time TradingView data.

Checks:
  1. TradingView account — sessionid cookie present and non-expired
  2. Chrome browser — accessible via rookiepy on the Docker host
  3. Cookies readable — loaded and injected into a live query

Run locally:   python3 scripts/check_requirements.py
Run in Docker: docker exec momentum_tv_collector python3 scripts/check_requirements.py
"""

import http.cookiejar
import os
import sys
import time
from pathlib import Path

PASS = "✅"
FAIL = "❌"
WARN = "⚠️ "
NA   = "➖"

results: list[tuple[str, bool, str]] = []

# Detect execution environment
IN_DOCKER = Path("/.dockerenv").exists()
ENV_LABEL = "Docker container" if IN_DOCKER else "host machine"


def check(label: str, ok: bool, detail: str) -> bool:
    results.append((label, ok, detail))
    icon = PASS if ok else FAIL
    print(f"  {icon}  {label}")
    print(f"       {detail}")
    return ok


def skip(label: str, reason: str) -> None:
    print(f"  {NA}  {label}")
    print(f"       {reason} (skipped)")


# ─────────────────────────────────────────────────────────────────────────────
print("\n══════════════════════════════════════════════════")
print("  TradingView Requirements Check")
print(f"  Running on: {ENV_LABEL}")
print("══════════════════════════════════════════════════\n")

# ── Requirement 1: Chrome browser readable via rookiepy ───────────────────────
print("1) Chrome browser accessible via rookiepy")
print("   (must run on the Docker HOST, not inside the container)\n")

chrome_jar: http.cookiejar.CookieJar | None = None

if IN_DOCKER:
    skip("Chrome browser", "running inside Docker — Chrome lives on the host")
else:
    try:
        import rookiepy
        loader = getattr(rookiepy, "chrome", None)
        if loader is None:
            check("Chrome browser", False, "rookiepy has no 'chrome' attribute on this platform")
        else:
            raw = loader(["tradingview.com"])
            chrome_jar = rookiepy.to_cookiejar(raw)
            count = sum(1 for _ in chrome_jar)
            if count > 0:
                check("Chrome browser", True, f"rookiepy.chrome() returned {count} cookies for tradingview.com")
            else:
                check("Chrome browser", False, "rookiepy.chrome() returned 0 cookies — is Chrome installed and has visited tradingview.com?")
    except Exception as e:
        check("Chrome browser", False, f"rookiepy.chrome() raised: {e}")

# ── Requirement 2: TradingView account (sessionid present + non-expired) ──────
print("\n2) TradingView account — session cookies\n")

TV_AUTH_COOKIES = {"sessionid", "sessionid_sign"}
found: dict[str, object] = {}

active_jar = chrome_jar

# Also check file-based jar if TV_COOKIES_FILE is set
cookies_file = os.getenv("TV_COOKIES_FILE", "").strip()
if cookies_file:
    p = Path(cookies_file)
    if p.exists():
        try:
            file_jar = http.cookiejar.MozillaCookieJar(str(p))
            file_jar.load(ignore_discard=True, ignore_expires=True)
            active_jar = file_jar
            print(f"   (using cookies file: {p})")
        except Exception as e:
            print(f"   (cookies file load failed: {e})")

if active_jar is not None:
    now = time.time()
    for cookie in active_jar:
        if cookie.domain and "tradingview" in cookie.domain:
            if cookie.name in TV_AUTH_COOKIES:
                expires = cookie.expires
                if expires and expires < now:
                    found[cookie.name] = "EXPIRED"
                else:
                    found[cookie.name] = "valid"

    has_session = "sessionid" in found and found["sessionid"] != "EXPIRED"
    has_sign    = "sessionid_sign" in found and found["sessionid_sign"] != "EXPIRED"

    if has_session and has_sign:
        check(
            "TradingView sessionid",
            True,
            "sessionid + sessionid_sign present and not expired → account is logged in",
        )
    elif "sessionid" in found and found["sessionid"] == "EXPIRED":
        check("TradingView sessionid", False, "sessionid is EXPIRED — log in to tradingview.com in Chrome and re-export cookies")
    elif not found:
        check(
            "TradingView sessionid",
            False,
            "No auth cookies found — log in to tradingview.com in Chrome first",
        )
    else:
        check("TradingView sessionid", False, f"Partial cookies: {found}")
else:
    check("TradingView sessionid", False, "No cookie jar available to inspect")

# ── Requirement 3: Cookies file exported and mounted (Docker path) ────────────
print("\n3) Cookies file exported for Docker\n")

docker_path = Path("/run/secrets/tradingview_cookies.txt")

# When running on the host, the secrets folder lives at the repo root (one level
# above the tv-collector/ directory that contains this script).
_script_dir = Path(__file__).resolve().parent          # .../tv-collector/scripts/
_repo_root  = _script_dir.parent.parent                # .../ai-stock-service/
host_path   = Path(os.getenv("TV_COOKIES_PATH",
                              str(_repo_root / "secrets" / "tradingview_cookies.txt")))

if docker_path.exists():
    # Running inside Docker
    try:
        djar = http.cookiejar.MozillaCookieJar(str(docker_path))
        djar.load(ignore_discard=True, ignore_expires=True)
        n = sum(1 for _ in djar)
        check("Cookies file (Docker)", True, f"Mounted at {docker_path} — {n} cookies")
    except Exception as e:
        check("Cookies file (Docker)", False, f"File exists but failed to load: {e}")
elif host_path.exists():
    # Running on host
    try:
        hjar = http.cookiejar.MozillaCookieJar(str(host_path))
        hjar.load(ignore_discard=True, ignore_expires=True)
        n = sum(1 for _ in hjar)
        check("Cookies file (host)", True, f"Found at {host_path} — {n} cookies — ready to mount into Docker")
    except Exception as e:
        check("Cookies file (host)", False, f"File exists but failed to load: {e}")
else:
    check(
        "Cookies file",
        False,
        f"Not found at {host_path} — run: python3 scripts/export_cookies.py",
    )

# ── Requirement 4: Live authenticated query ───────────────────────────────────
print("\n4) Live authenticated query to TradingView\n")

# Pick the best available jar
query_jar: http.cookiejar.CookieJar = http.cookiejar.CookieJar()
for j in [active_jar, chrome_jar]:
    if j is not None:
        query_jar = j
        break

try:
    from tradingview_screener.query import Query
    total, df = (
        Query()
        .select("name", "close", "RSI")
        .set_markets("america")
        .limit(2)
        .get_scanner_data(cookies=query_jar)
    )
    check(
        "Live query",
        True,
        f"total_count={total:,}  sample={df['ticker'].tolist()}",
    )
except Exception as e:
    check("Live query", False, str(e)[:120])

# ── Summary ───────────────────────────────────────────────────────────────────
print("\n══════════════════════════════════════════════════")
passed = sum(1 for _, ok, _ in results if ok)
total_checks = len(results)
all_ok = passed == total_checks

print(f"  Result: {passed}/{total_checks} checks passed")
print("══════════════════════════════════════════════════\n")

if all_ok:
    print(f"{PASS}  All requirements met — real-time data is active\n")
    sys.exit(0)
else:
    failed = [label for label, ok, _ in results if not ok]
    print(f"{FAIL}  Failed checks: {', '.join(failed)}")
    print()
    print("  Quick fix:")
    print("    1. Log in to https://www.tradingview.com in Chrome")
    print("    2. python3 scripts/export_cookies.py")
    print("    3. docker compose restart tv-collector")
    print()
    sys.exit(1)





