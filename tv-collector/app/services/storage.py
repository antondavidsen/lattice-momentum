"""
app/services/storage.py
────────────────────────
Raw snapshot storage service.

Saves a pandas DataFrame as a CSV file under::

    <RAW_DATA_DIR>/YYYY-MM-DD/<screener_name>.csv

One directory per calendar date, one file per screener.
Folders are created automatically on first write.
"""

from __future__ import annotations

import os
from datetime import date
from pathlib import Path

import pandas as pd
import structlog

log = structlog.get_logger(__name__)

# Default base directory — overridden by RAW_DATA_DIR env var at runtime.
_DEFAULT_RAW_DIR = Path("./data/raw")


def save_raw_snapshot(
    df: pd.DataFrame,
    screener_name: str,
    *,
    base_dir: Path | str | None = None,
    run_date: date | None = None,
) -> Path:
    """
    Persist a screener DataFrame as a dated CSV snapshot.

    Directory layout::

        <base_dir>/
        └── 2026-04-12/
            ├── momentum.csv
            ├── episodic_pivot.csv
            └── market_leaders.csv

    :param df:            DataFrame returned by a screener's ``run()`` function.
    :param screener_name: Slug used as the filename, e.g. ``"momentum"``.
    :param base_dir:      Root directory for raw snapshots.
                          Falls back to the ``RAW_DATA_DIR`` env var, then
                          ``./data/raw``.
    :param run_date:      Date used for the sub-directory name.
                          Defaults to today (UTC).
    :returns: Resolved ``Path`` of the written file.
    :raises ValueError: if ``screener_name`` is empty.
    :raises OSError:    if the directory cannot be created or the file written.
    """
    if not screener_name or not screener_name.strip():
        raise ValueError("screener_name must not be empty")

    resolved_base = _resolve_base_dir(base_dir)
    today = run_date or date.today()
    output_dir = resolved_base / today.isoformat()

    output_dir.mkdir(parents=True, exist_ok=True)

    output_path = output_dir / f"{screener_name.strip()}.csv"

    df.to_csv(output_path, index=False)

    log.info(
        "storage.snapshot_saved",
        screener=screener_name,
        path=str(output_path),
        rows=len(df),
        columns=len(df.columns),
        date=today.isoformat(),
    )

    return output_path


def _resolve_base_dir(override: Path | str | None) -> Path:
    """
    Priority: explicit argument → RAW_DATA_DIR env var → ./data/raw default.
    """
    if override is not None:
        return Path(override)

    env_val = os.getenv("RAW_DATA_DIR")
    if env_val:
        return Path(env_val)

    return _DEFAULT_RAW_DIR

