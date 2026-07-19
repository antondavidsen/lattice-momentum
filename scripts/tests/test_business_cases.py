"""Tier 2: business-case integration tests, driven through the in-process
`run(TrainConfig) -> TrainResult` seam.

Why in-process instead of subprocess:
- We call `tp.run(config)` directly (same interpreter) and get a `TrainResult`
  object back, so we assert on the *actual decision* (exit_code, model_written,
  the metrics dict) rather than scraping stdout or exit codes from a child
  process. Richer assertions, faster, and a real stack trace on failure.
- We STILL check the side effects on disk (which files exist), because "did it
  write the model artifact?" is part of the business contract.

Go -> pytest translations new in this file:
- `pytest.importorskip("xgboost")` == a build tag / `t.Skip()` guard: if the
  tree engine isn't installed, skip instead of failing. This is how one suite
  stays green whether a machine has xgboost, lightgbm, both, or neither.
- `@pytest.fixture(params=[...])` fans a single test out over multiple backends,
  like running the same table-driven test against several implementations of an
  interface.
- These are the expensive tests, so they carry the `integration` marker
  (declared in pytest.ini). Run only the fast tier with:  pytest -m 'not integration'
"""
from __future__ import annotations

import json
from pathlib import Path
from typing import Any, Callable

import pandas as pd
import pytest

import train_premonition as tp

# Everything in this module is an integration test.
pytestmark = pytest.mark.integration


@pytest.fixture(params=["xgboost", "lightgbm"])
def backend(request: pytest.FixtureRequest) -> str:
    """Yield each installed backend; skip the ones that aren't importable.
    A test that depends on `backend` is therefore run once per available
    engine, and simply skipped when neither is present."""
    pytest.importorskip(request.param)
    return request.param


def _config(
    csv: Path,
    out_paths: dict[str, Path],
    backend: str,
    **overrides: Any,
) -> tp.TrainConfig:
    """Small builder so each test states only what differs from the defaults.
    (The Go instinct: a test-local options struct with sensible zero values.)"""
    params: dict[str, Any] = dict(
        input_csv=str(csv),
        output_model=str(out_paths["model"]),
        output_metrics=str(out_paths["metrics"]),
        backend=backend,
        min_auc=0.60,
        cv_folds=4,
        date_col="event_date",
        group_col="ticker",
        purge_groups=False,
        embargo=0,
    )
    params.update(overrides)
    return tp.TrainConfig(**params)


# --------------------------------------------------------------------------- #
# Business case 1: real signal -> the gate passes and a model is shipped.
# --------------------------------------------------------------------------- #
def test_signal_dataset_ships_a_model(
    backend: str,
    make_dataset: Callable[..., pd.DataFrame],
    write_csv: Callable[..., Path],
    out_paths: dict[str, Path],
) -> None:
    csv = write_csv(make_dataset(mode="signal", n_rows=800, seed=1))

    result = tp.run(_config(csv, out_paths, backend, min_auc=0.60))

    # The decision:
    assert result.exit_code == 0
    assert result.model_written is True
    assert result.metrics["threshold_exceeded"] is True
    # A genuine edge should comfortably clear a modest bar.
    assert float(result.metrics["auc_gate"]) > 0.60

    # The side effects: both artifacts on disk, and the metrics file matches
    # the returned metrics (no drift between what we return and what we write).
    assert out_paths["model"].exists()
    assert out_paths["metrics"].exists()
    on_disk = json.loads(out_paths["metrics"].read_text())
    assert on_disk["threshold_exceeded"] is True
    assert on_disk["auc_gate"] == pytest.approx(result.metrics["auc_gate"])


# --------------------------------------------------------------------------- #
# Business case 2: no signal -> the gate refuses and writes NO model.
# This is the most important safety property of the whole tool.
# --------------------------------------------------------------------------- #
def test_noise_dataset_is_rejected(
    backend: str,
    make_dataset: Callable[..., pd.DataFrame],
    write_csv: Callable[..., Path],
    out_paths: dict[str, Path],
) -> None:
    csv = write_csv(make_dataset(mode="noise", n_rows=800, seed=2))

    result = tp.run(_config(csv, out_paths, backend, min_auc=0.60))

    assert result.exit_code == 1
    assert result.model_written is False
    assert result.metrics["threshold_exceeded"] is False
    # Pure noise cannot be ranked: AUC hovers around chance.
    assert float(result.metrics["auc_gate"]) < 0.60

    # The critical side effect: the model artifact must NOT exist...
    assert not out_paths["model"].exists()
    # ...but the metrics file is still written for diagnostics.
    assert out_paths["metrics"].exists()


# --------------------------------------------------------------------------- #
# Business case 3: leakage integrity. A dataset whose label is only
# memorizable per-group (no general rule) looks predictive when the same
# groups leak across the split, and collapses once --purge-groups removes them.
# --------------------------------------------------------------------------- #
def test_purge_groups_removes_memorization_leakage(
    backend: str,
    make_dataset: Callable[..., pd.DataFrame],
    write_csv: Callable[..., Path],
    out_paths: dict[str, Path],
    tmp_path: Path,
) -> None:
    csv = write_csv(
        make_dataset(mode="group_memorizable", n_rows=1000, n_groups=25, seed=3)
    )

    # min_auc=0.0 so BOTH runs complete and we can compare their honest numbers
    # rather than one short-circuiting at the gate.
    leaky = tp.run(
        _config(
            csv,
            out_paths,
            backend,
            purge_groups=False,
            min_auc=0.0,
            output_model=str(tmp_path / "leaky_model.json"),
            output_metrics=str(tmp_path / "leaky_metrics.json"),
        )
    )
    purged = tp.run(
        _config(
            csv,
            out_paths,
            backend,
            purge_groups=True,
            min_auc=0.0,
            output_model=str(tmp_path / "purged_model.json"),
            output_metrics=str(tmp_path / "purged_metrics.json"),
        )
    )

    leaky_auc = float(leaky.metrics["auc_gate"])
    purged_auc = float(purged.metrics["auc_gate"])

    # 1) Leakage massively inflates the apparent AUC.
    assert leaky_auc > purged_auc + 0.15
    # 2) The honest (purged) AUC is near chance -- there was never a real edge.
    assert purged_auc < 0.62
    # 3) The business consequence: a realistic gate that the leaky model sails
    #    through would correctly REJECT the honest one.
    realistic_bar = 0.65
    assert leaky_auc > realistic_bar   # would have shipped a fake model
    assert purged_auc < realistic_bar  # purging stops it
