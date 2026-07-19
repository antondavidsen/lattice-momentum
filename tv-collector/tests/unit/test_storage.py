"""
tests/unit/test_storage.py
───────────────────────────
Unit tests for the storage service (save_raw_snapshot / _resolve_base_dir).

Covers:
- Saving a DataFrame as dated CSV snapshot
- Directory creation for date subdirectories
- Handling of empty screener_name (ValueError)
- base_dir override and env-var fallback
- File content correctness (CSV includes all rows and columns)
"""

from __future__ import annotations

import os
from datetime import date
from pathlib import Path

import pandas as pd
import pytest

from app.services.storage import save_raw_snapshot, _resolve_base_dir


class TestResolveBaseDir:
    """_resolve_base_dir resolves the base directory with correct priority."""

    def test_explicit_override_used(self) -> None:
        result = _resolve_base_dir("/custom/path")
        assert result == Path("/custom/path")

    def test_env_var_used_when_no_override(self, monkeypatch: pytest.MonkeyPatch) -> None:
        monkeypatch.setenv("RAW_DATA_DIR", "/env/path")
        result = _resolve_base_dir(None)
        assert result == Path("/env/path")

    def test_default_when_no_override_and_no_env(self, monkeypatch: pytest.MonkeyPatch) -> None:
        monkeypatch.delenv("RAW_DATA_DIR", raising=False)
        result = _resolve_base_dir(None)
        assert result == Path("./data/raw")

    def test_override_takes_priority_over_env(self, monkeypatch: pytest.MonkeyPatch) -> None:
        monkeypatch.setenv("RAW_DATA_DIR", "/env/path")
        result = _resolve_base_dir("/custom/path")
        assert result == Path("/custom/path")

    def test_path_object_accepted(self) -> None:
        result = _resolve_base_dir(Path("/path/object"))
        assert result == Path("/path/object")


class TestSaveRawSnapshot:
    """save_raw_snapshot writes CSV files in the expected directory layout."""

    def test_saves_csv_to_date_subdirectory(self, tmp_path: Path) -> None:
        df = pd.DataFrame({"ticker": ["AAPL"], "close": [150.0]})
        result_path = save_raw_snapshot(df, "momentum", base_dir=tmp_path, run_date=date(2026, 6, 1))
        expected = tmp_path / "2026-06-01" / "momentum.csv"
        assert result_path == expected
        assert expected.exists()
        assert expected.is_file()

    def test_csv_content_correct(self, tmp_path: Path) -> None:
        df = pd.DataFrame({"ticker": ["AAPL", "MSFT"], "close": [150.0, 420.0]})
        save_raw_snapshot(df, "momentum", base_dir=tmp_path, run_date=date(2026, 6, 1))
        csv_path = tmp_path / "2026-06-01" / "momentum.csv"
        content = csv_path.read_text()
        assert "AAPL" in content
        assert "MSFT" in content
        assert "150.0" in content
        assert "420.0" in content

    def test_creates_date_directory_automatically(self, tmp_path: Path) -> None:
        df = pd.DataFrame({"ticker": ["AAPL"]})
        save_raw_snapshot(df, "momentum", base_dir=tmp_path, run_date=date(2026, 12, 25))
        assert (tmp_path / "2026-12-25").is_dir()

    def test_defaults_to_today_when_no_date(self, tmp_path: Path) -> None:
        df = pd.DataFrame({"ticker": ["AAPL"]})
        result_path = save_raw_snapshot(df, "momentum", base_dir=tmp_path)
        today_iso = date.today().isoformat()
        expected = tmp_path / today_iso / "momentum.csv"
        assert result_path == expected
        assert expected.exists()

    def test_empty_screener_name_raises(self, tmp_path: Path) -> None:
        df = pd.DataFrame({"ticker": ["AAPL"]})
        with pytest.raises(ValueError, match="screener_name must not be empty"):
            save_raw_snapshot(df, "", base_dir=tmp_path)

    def test_whitespace_screener_name_raises(self, tmp_path: Path) -> None:
        df = pd.DataFrame({"ticker": ["AAPL"]})
        with pytest.raises(ValueError, match="screener_name must not be empty"):
            save_raw_snapshot(df, "   ", base_dir=tmp_path)

    def test_multiple_screeners_write_separate_files(self, tmp_path: Path) -> None:
        df1 = pd.DataFrame({"ticker": ["AAPL"]})
        df2 = pd.DataFrame({"ticker": ["MSFT"]})
        save_raw_snapshot(df1, "momentum", base_dir=tmp_path, run_date=date(2026, 6, 1))
        save_raw_snapshot(df2, "episodic_pivot", base_dir=tmp_path, run_date=date(2026, 6, 1))
        assert (tmp_path / "2026-06-01" / "momentum.csv").exists()
        assert (tmp_path / "2026-06-01" / "episodic_pivot.csv").exists()

    def test_screener_name_stripped(self, tmp_path: Path) -> None:
        """Whitespace in screener_name is stripped from the filename."""
        df = pd.DataFrame({"ticker": ["AAPL"]})
        save_raw_snapshot(df, "  momentum  ", base_dir=tmp_path, run_date=date(2026, 6, 1))
        assert (tmp_path / "2026-06-01" / "momentum.csv").exists()
        assert not (tmp_path / "2026-06-01" / "  momentum  .csv").exists()

    def test_existing_directory_not_overwritten(self, tmp_path: Path) -> None:
        """Writing to an existing date directory should not raise."""
        df1 = pd.DataFrame({"ticker": ["AAPL"]})
        df2 = pd.DataFrame({"ticker": ["MSFT"]})
        save_raw_snapshot(df1, "momentum", base_dir=tmp_path, run_date=date(2026, 6, 1))
        save_raw_snapshot(df2, "momentum", base_dir=tmp_path, run_date=date(2026, 6, 1))
        csv_path = tmp_path / "2026-06-01" / "momentum.csv"
        assert csv_path.exists()
        content = csv_path.read_text()
        # Last write wins — contains MSFT
        assert "MSFT" in content

    def test_empty_dataframe_saves(self, tmp_path: Path) -> None:
        """An empty DataFrame saves a CSV with headers only."""
        df = pd.DataFrame({"ticker": pd.Series(dtype=str), "close": pd.Series(dtype=float)})
        save_raw_snapshot(df, "momentum", base_dir=tmp_path, run_date=date(2026, 6, 1))
        csv_path = tmp_path / "2026-06-01" / "momentum.csv"
        assert csv_path.exists()
        content = csv_path.read_text().strip()
        assert content == "ticker,close"