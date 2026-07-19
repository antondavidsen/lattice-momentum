#!/usr/bin/env python3
"""
Quick validation script for Prometheus /metrics endpoint.

Run this after starting the tv-collector service:
    python3 verify_metrics.py
"""

import sys
import time
from pathlib import Path

# Add app to path
repo_root = Path(__file__).parent.parent
sys.path.insert(0, str(repo_root))

try:
    import httpx
    import structlog
except ImportError as e:
    print(f"ERROR: Missing dependency: {e}")
    print("Install with: pip install httpx structlog")
    sys.exit(1)


def test_metrics_endpoint(base_url: str = "http://localhost:8001") -> None:
    """
    Test that the /metrics endpoint returns valid Prometheus exposition format.

    Checks:
      1. Endpoint returns 200
      2. Content-Type is text/plain
      3. Response contains # HELP lines
      4. Response contains # TYPE lines
      5. Response contains tv_collector_* metrics
    """
    log = structlog.get_logger(__name__)

    log.info("test_metrics_endpoint.start", url=f"{base_url}/metrics")

    client = httpx.Client(timeout=10)

    try:
        response = client.get(f"{base_url}/metrics")

        # Check status
        if response.status_code != 200:
            log.error(
                "test_metrics_endpoint.failed",
                reason="status_code",
                got=response.status_code,
                expected=200,
            )
            return False

        log.info("test_metrics_endpoint.status_ok", code=200)

        # Check content type
        content_type = response.headers.get("content-type", "").lower()
        if "text/plain" not in content_type:
            log.warning(
                "test_metrics_endpoint.unexpected_content_type",
                got=content_type,
            )

        # Check content
        text = response.text
        if not text:
            log.error("test_metrics_endpoint.empty_response")
            return False

        checks = [
            ("# HELP", "Prometheus HELP lines"),
            ("# TYPE", "Prometheus TYPE lines"),
            ("tv_collector_", "Custom tv_collector metrics"),
        ]

        all_ok = True
        for marker, description in checks:
            if marker in text:
                log.info("test_metrics_endpoint.check_ok", check=description)
            else:
                log.error("test_metrics_endpoint.check_failed", check=description)
                all_ok = False

        if all_ok:
            log.info(
                "test_metrics_endpoint.success",
                response_size=len(text),
                hint="All checks passed. Metrics endpoint is working.",
            )
            print("\n✓ All checks passed!")
            print(f"  Response size: {len(text)} bytes")
            print(f"  Sample metrics:\n{text[:500]}...")
            return True
        else:
            log.error("test_metrics_endpoint.checks_failed")
            print("\n✗ Some checks failed. See logs above.")
            return False

    except httpx.ConnectError:
        log.error(
            "test_metrics_endpoint.connection_error",
            hint="Is the tv-collector service running on port 8001?",
        )
        print(f"\n✗ Cannot connect to {base_url}")
        print("  Is the tv-collector service running? docker-compose ps")
        return False
    except Exception as exc:
        log.exception("test_metrics_endpoint.unexpected_error", error=str(exc))
        print(f"\n✗ Unexpected error: {exc}")
        return False
    finally:
        client.close()


if __name__ == "__main__":
    import sys

    success = test_metrics_endpoint()
    sys.exit(0 if success else 1)

