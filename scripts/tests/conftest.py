"""Shared pytest fixtures for the train_premonition test suite.

Go -> pytest mental model for this file:
- `conftest.py` is pytest's magic, auto-discovered file. Any fixture defined
  here is injected into every test in this directory tree *by name*, with no
  imports needed. Think of it as a package-scoped `TestMain` plus a bag of
  reusable test constructors/helpers.
- A `@pytest.fixture` is dependency injection. A test that has a parameter
  named `make_dataset` receives whatever the `make_dataset` fixture returns.
  This is how pytest replaces the manual `setup := newTestServer(t)` you write
  at the top of Go tests.
- `tmp_path` is a built-in fixture == Go's `t.TempDir()`: a fresh temp dir per
  test, auto-cleaned.
"""
from __future__ import annotations

import sys
from pathlib import Path
from typing import Callable

import numpy as np
import pandas as pd
import pytest

# Make the script importable no matter what directory pytest is launched from.
# (Equivalent to putting the package on your GOPATH/module path.)
sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

import train_premonition as tp  # noqa: E402  (must follow the sys.path tweak)

# A "factory fixture" returns a *function*, so each test can build the exact
# dataset shape it needs. This is the single most useful pattern for
# complicated tests: one well-parameterised constructor instead of a dozen
# near-identical hand-written tables.
DatasetFactory = Callable[..., pd.DataFrame]
CsvWriter = Callable[..., Path]


def _daily_dates(n_rows: int) -> np.ndarray:
    """Monotonic daily timestamps so the walk-forward split has a real time
    axis to sort on."""
    start = np.datetime64("2025-01-01")
    return start + np.arange(n_rows).astype("timedelta64[D]")


def _make_dataset(
    mode: str = "signal",
    n_rows: int = 600,
    n_groups: int = 40,
    within_group_noise: float = 0.25,
    seed: int = 7,
) -> pd.DataFrame:
    """Build a synthetic labelled dataset with a controllable data-generating
    process. `mode` decides what kind of relationship exists between features
    and label -- this is what lets each business-case test assert a *specific*
    behaviour of the training gate.

    Modes:
      - "signal": label is a genuine (noisy) function of the features. A real
        model should learn it and clear the AUC gate.
      - "noise": label is independent of the features. AUC ~ 0.5, gate blocks.
      - "group_memorizable": each group (ticker) has a fixed feature signature
        and a fixed label, but there is NO global feature->label rule. If the
        same tickers appear in train and validation, the model memorises the
        signature and looks great (leakage). With --purge-groups it can't, and
        the fake edge disappears. This is the anti-leakage test dataset.
    """
    rng = np.random.default_rng(seed)
    feats = tp.REQUIRED_FEATURES
    n_feats = len(feats)

    if mode == "signal":
        X = rng.normal(size=(n_rows, n_feats))
        weights = rng.normal(size=n_feats)
        logit = X @ weights
        prob = 1.0 / (1.0 + np.exp(-logit))
        label = (rng.uniform(size=n_rows) < prob).astype(np.float64)
        group_ids = rng.integers(0, n_groups, size=n_rows)

    elif mode == "noise":
        X = rng.normal(size=(n_rows, n_feats))
        label = (rng.uniform(size=n_rows) < 0.5).astype(np.float64)
        group_ids = rng.integers(0, n_groups, size=n_rows)

    elif mode == "group_memorizable":
        group_ids = rng.integers(0, n_groups, size=n_rows)
        signatures = rng.normal(size=(n_groups, n_feats))          # per-ticker fingerprint
        group_label = rng.integers(0, 2, size=n_groups).astype(np.float64)
        X = signatures[group_ids] + within_group_noise * rng.normal(size=(n_rows, n_feats))
        label = group_label[group_ids]

    else:
        raise ValueError(f"unknown mode: {mode!r}")

    df = pd.DataFrame(X, columns=feats)
    df[tp.TARGET_COL] = label
    df["event_date"] = _daily_dates(n_rows)
    df["ticker"] = [f"T{g:03d}" for g in group_ids]
    return df


@pytest.fixture
def make_dataset() -> DatasetFactory:
    """Inject the dataset factory. Tests call `make_dataset(mode=..., ...)`."""
    return _make_dataset


@pytest.fixture
def write_csv(tmp_path: Path) -> CsvWriter:
    """Return a helper that persists a DataFrame to a CSV inside this test's
    private temp dir and hands back the path. Used by the CLI/business-case
    tests in Step 2.
    """
    def _write(df: pd.DataFrame, name: str = "training_data.csv") -> Path:
        path = tmp_path / name
        df.to_csv(path, index=False)
        return path

    return _write


@pytest.fixture
def out_paths(tmp_path: Path) -> dict[str, Path]:
    """Canonical output locations for the trainer, isolated per test."""
    return {
        "model": tmp_path / "model.json",
        "metrics": tmp_path / "metrics.json",
    }
