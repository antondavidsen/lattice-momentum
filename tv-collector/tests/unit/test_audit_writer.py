"""
tests/unit/test_audit_writer.py
─────────────────────────────────
Unit tests for the audit_writer module.

Tests cover:
  - write_audit with empty list (no-op)
  - write_audit POST success to backend
  - write_audit with non-2xx response falls back to local file
  - write_audit with connection error falls back to local file
  - write_audit with timeout falls back to local file
  - Fallback file is written with correct content
  - Fallback appends to existing file
  - Fallback file creation when directory does not exist
"""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any
from unittest.mock import MagicMock, patch

import httpx
import pytest

from app.screeners.audit_writer import AUDIT_ENDPOINT, FALLBACK_DIR, write_audit


class TestWriteAuditEmpty:
    """write_audit with empty eliminations list."""

    def test_empty_list_does_nothing(self, make_elimination_record: Any) -> None:
        """An empty list should not make any HTTP requests."""
        with patch("app.screeners.audit_writer.httpx.Client") as mock_client_cls:
            write_audit([], "http://backend:8080")
        mock_client_cls.assert_not_called()


class TestWriteAuditSuccess:
    """write_audit successfully POSTs to the backend."""

    def test_posts_to_correct_endpoint(self, make_elimination_record: Any) -> None:
        """The POST should hit {backend_url}/api/v1/stage1-eliminations."""
        records = [make_elimination_record(ticker="AAPL")]

        with patch("app.screeners.audit_writer.httpx.Client") as mock_client_cls:
            mock_client = MagicMock()
            mock_client_cls.return_value.__enter__.return_value = mock_client
            mock_client.post.return_value.is_success = True
            mock_client.post.return_value.status_code = 200

            write_audit(records, "http://backend:8080")

        mock_client.post.assert_called_once_with(
            "http://backend:8080/api/v1/stage1-eliminations",
            json=[
                {
                    "ticker": "AAPL",
                    "screener": "momentum",
                    "rule": "climax_top",
                    "tier": "hard_eliminate",
                    "metrics": {"today_range": 8.0, "atr": 2.0},
                    "reason": "test elimination",
                    "date": "2026-06-01",
                }
            ],
        )

    def test_success_does_not_write_fallback(self, make_elimination_record: Any, tmp_path: Path) -> None:
        """On 200 response, no fallback file should be written."""
        records = [make_elimination_record(ticker="AAPL")]

        with patch("app.screeners.audit_writer.httpx.Client") as mock_client_cls:
            mock_client = MagicMock()
            mock_client_cls.return_value.__enter__.return_value = mock_client
            mock_client.post.return_value.is_success = True
            mock_client.post.return_value.status_code = 200

            with patch("app.screeners.audit_writer.Path") as mock_path:
                write_audit(records, "http://backend:8080")
                mock_path.assert_not_called()


class TestWriteAuditNon2xx:
    """write_audit with non-2xx response falls back to local file."""

    def test_non_2xx_triggers_fallback(self, make_elimination_record: Any, tmp_path: Path) -> None:
        """A 500 response should fall back to writing a local file."""
        records = [make_elimination_record(ticker="TSLA", date="2026-06-15")]
        fallback_dir = tmp_path / FALLBACK_DIR

        with patch("app.screeners.audit_writer.httpx.Client") as mock_client_cls:
            mock_client = MagicMock()
            mock_client_cls.return_value.__enter__.return_value = mock_client
            mock_client.post.return_value.is_success = False
            mock_client.post.return_value.status_code = 500
            mock_client.post.return_value.text = "Internal Server Error"

            with patch("app.screeners.audit_writer.FALLBACK_DIR", str(fallback_dir)):
                write_audit(records, "http://backend:8080")

        fallback_path = fallback_dir / "stage1_audit_2026-06-15.json"
        assert fallback_path.exists()

        with open(fallback_path) as f:
            data = json.load(f)
        assert len(data) == 1
        assert data[0]["ticker"] == "TSLA"


class TestWriteAuditConnectionError:
    """write_audit with connection error falls back to local file."""

    def test_connection_error_triggers_fallback(self, make_elimination_record: Any, tmp_path: Path) -> None:
        """A ConnectError should fall back to writing a local file."""
        records = [make_elimination_record(ticker="GOOGL", date="2026-07-01")]
        fallback_dir = tmp_path / FALLBACK_DIR

        with patch("app.screeners.audit_writer.httpx.Client") as mock_client_cls:
            mock_client = MagicMock()
            mock_client_cls.return_value.__enter__.return_value = mock_client
            mock_client.post.side_effect = httpx.ConnectError("Connection refused")

            with patch("app.screeners.audit_writer.FALLBACK_DIR", str(fallback_dir)):
                write_audit(records, "http://backend:8080")

        fallback_path = fallback_dir / "stage1_audit_2026-07-01.json"
        assert fallback_path.exists()

        with open(fallback_path) as f:
            data = json.load(f)
        assert len(data) == 1
        assert data[0]["ticker"] == "GOOGL"


class TestWriteAuditTimeout:
    """write_audit with timeout falls back to local file."""

    def test_timeout_triggers_fallback(self, make_elimination_record: Any, tmp_path: Path) -> None:
        """A TimeoutException should fall back to writing a local file."""
        records = [make_elimination_record(ticker="META", date="2026-07-02")]
        fallback_dir = tmp_path / FALLBACK_DIR

        with patch("app.screeners.audit_writer.httpx.Client") as mock_client_cls:
            mock_client = MagicMock()
            mock_client_cls.return_value.__enter__.return_value = mock_client
            mock_client.post.side_effect = httpx.TimeoutException("Request timed out")

            with patch("app.screeners.audit_writer.FALLBACK_DIR", str(fallback_dir)):
                write_audit(records, "http://backend:8080")

        fallback_path = fallback_dir / "stage1_audit_2026-07-02.json"
        assert fallback_path.exists()

        with open(fallback_path) as f:
            data = json.load(f)
        assert len(data) == 1
        assert data[0]["ticker"] == "META"


class TestFallbackWrite:
    """Fallback file writing behavior."""

    def test_fallback_file_has_correct_content(self, make_elimination_record: Any, tmp_path: Path) -> None:
        """The fallback file should contain properly formatted JSON."""
        records = [
            make_elimination_record(ticker="AAPL", rule="climax_top", date="2026-06-01"),
            make_elimination_record(ticker="MSFT", rule="volume_climax", date="2026-06-01"),
        ]
        fallback_dir = tmp_path / FALLBACK_DIR

        with patch("app.screeners.audit_writer.httpx.Client") as mock_client_cls:
            mock_client = MagicMock()
            mock_client_cls.return_value.__enter__.return_value = mock_client
            mock_client.post.side_effect = httpx.ConnectError("offline")

            with patch("app.screeners.audit_writer.FALLBACK_DIR", str(fallback_dir)):
                write_audit(records, "http://backend:8080")

        fallback_path = fallback_dir / "stage1_audit_2026-06-01.json"
        assert fallback_path.exists()

        with open(fallback_path) as f:
            data = json.load(f)
        assert len(data) == 2
        assert data[0]["ticker"] == "AAPL"
        assert data[0]["rule"] == "climax_top"
        assert data[1]["ticker"] == "MSFT"
        assert data[1]["rule"] == "volume_climax"

    def test_fallback_appends_to_existing(self, make_elimination_record: Any, tmp_path: Path) -> None:
        """Running fallback twice should append records."""
        records = [make_elimination_record(ticker="AAPL", date="2026-06-01")]
        fallback_dir = tmp_path / FALLBACK_DIR

        def _run_fallback() -> None:
            with patch("app.screeners.audit_writer.httpx.Client") as mock_client_cls:
                mock_client = MagicMock()
                mock_client_cls.return_value.__enter__.return_value = mock_client
                mock_client.post.side_effect = httpx.ConnectError("offline")
                with patch("app.screeners.audit_writer.FALLBACK_DIR", str(fallback_dir)):
                    write_audit(records, "http://backend:8080")

        # First write
        _run_fallback()

        # Second write
        records2 = [make_elimination_record(ticker="MSFT", date="2026-06-01")]
        with patch("app.screeners.audit_writer.httpx.Client") as mock_client_cls:
            mock_client = MagicMock()
            mock_client_cls.return_value.__enter__.return_value = mock_client
            mock_client.post.side_effect = httpx.ConnectError("offline")
            with patch("app.screeners.audit_writer.FALLBACK_DIR", str(fallback_dir)):
                write_audit(records2, "http://backend:8080")

        fallback_path = fallback_dir / "stage1_audit_2026-06-01.json"
        with open(fallback_path) as f:
            data = json.load(f)
        assert len(data) == 2
        assert data[0]["ticker"] == "AAPL"
        assert data[1]["ticker"] == "MSFT"

    def test_fallback_creates_directory(self, make_elimination_record: Any, tmp_path: Path) -> None:
        """Fallback should create the logs directory if it does not exist."""
        records = [make_elimination_record(ticker="NFLX", date="2026-06-01")]
        fallback_dir = tmp_path / "nonexistent" / "logs"

        with patch("app.screeners.audit_writer.httpx.Client") as mock_client_cls:
            mock_client = MagicMock()
            mock_client_cls.return_value.__enter__.return_value = mock_client
            mock_client.post.side_effect = httpx.ConnectError("offline")

            with patch("app.screeners.audit_writer.FALLBACK_DIR", str(fallback_dir)):
                write_audit(records, "http://backend:8080")

        fallback_path = fallback_dir / "stage1_audit_2026-06-01.json"
        assert fallback_path.exists()