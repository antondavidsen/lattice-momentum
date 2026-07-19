#!/usr/bin/env python3
"""Premonition model trainer - XGBoost/LightGBM weight refit sidecar.

Reads labelled data from CSV, trains a gradient-boosted tree model using
time-aware, group-purged walk-forward validation, and exports the model +
metrics JSON if the out-of-sample AUC clears the gate.

Usage:
    python3 train_premonition.py \
        --input-csv /tmp/training_data.csv \
        --output-model /tmp/model_xgboost_2026-05-01.json \
        --output-metrics /tmp/metrics_2026-05-01.json \
        --backend xgboost \
        --min-auc 0.65 \
        --date-col event_date \
        --group-col ticker \
        --purge-groups

Exit codes:
    0 - success (model written)
    1 - gate not cleared (no model written)
    2 - missing features / bad schema
    3 - insufficient data (< 200 rows)
"""
from __future__ import annotations

import argparse
import json
import os
import sys
import warnings
from dataclasses import dataclass
from typing import TYPE_CHECKING, Union

import numpy as np
import numpy.typing as npt
import pandas as pd

if TYPE_CHECKING:  # type-only imports; no runtime cost, lets mypy resolve names
    import lightgbm as lgb
    import xgboost as xgb

    Model = Union["xgb.Booster", "lgb.Booster"]

FloatArray = npt.NDArray[np.float64]

REQUIRED_FEATURES: list[str] = [
    "event_quality_score",
    "volume_spike_score",
    "follow_through_score",
    "trend_alignment_score",
    "earnings_quality_score",
    "options_flow_score",
    "float_rotation_score",
    "narrative_velocity_score",
    "regime_multiplier",
    "sector_multiplier",
]
TARGET_COL = "label"  # 1.0 = positive outcome, 0.0 = negative


@dataclass
class TrainConfig:
    """All inputs to a training run. Built from CLI args in `main`, or
    constructed directly by tests -- this is the in-process seam."""

    input_csv: str
    output_model: str
    output_metrics: str
    backend: str = "xgboost"
    min_auc: float = 0.65
    cv_folds: int = 5
    date_col: str | None = None
    group_col: str | None = None
    purge_groups: bool = False
    embargo: int = 0


@dataclass
class TrainResult:
    """The outcome of a run, returned instead of calling sys.exit so callers
    (main and tests) can inspect the decision directly."""

    exit_code: int
    metrics: dict[str, object]
    model_written: bool
    model_path: str | None = None
    metrics_path: str | None = None


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Train XGBoost/LightGBM premonition model from labelled data."
    )
    parser.add_argument("--input-csv", required=True, help="Path to labelled training CSV")
    parser.add_argument("--output-model", required=True, help="Path to write trained model JSON")
    parser.add_argument("--output-metrics", required=True, help="Path to write metrics JSON")
    parser.add_argument(
        "--backend",
        default="xgboost",
        choices=["xgboost", "lightgbm"],
        help="Gradient boosting backend",
    )
    parser.add_argument(
        "--min-auc",
        type=float,
        default=0.65,
        help="Minimum (mean - std) OOS AUC to accept model (default: 0.65)",
    )
    parser.add_argument(
        "--cv-folds",
        type=int,
        default=5,
        help="Number of forward-chaining folds (default: 5)",
    )
    parser.add_argument(
        "--date-col",
        default=None,
        help="Column to sort by for time-aware folds. If omitted, existing row "
        "order is assumed to be chronological.",
    )
    parser.add_argument(
        "--group-col",
        default=None,
        help="Grouping column (e.g. ticker) used for leakage purging.",
    )
    parser.add_argument(
        "--purge-groups",
        action="store_true",
        help="Drop training rows that share a group with the validation fold.",
    )
    parser.add_argument(
        "--embargo",
        type=int,
        default=0,
        help="Rows to drop from the end of each training window as an embargo gap.",
    )
    return parser.parse_args()


def validate_data(df: pd.DataFrame, date_col: str | None, group_col: str | None) -> None:
    """Validate schema, row count, and that feature/target values are usable."""
    missing = [f for f in REQUIRED_FEATURES if f not in df.columns]
    if missing:
        print(f"ERROR: Missing features: {', '.join(missing)}", file=sys.stderr)
        sys.exit(2)

    if TARGET_COL not in df.columns:
        print(f"ERROR: Missing target column '{TARGET_COL}'", file=sys.stderr)
        sys.exit(2)

    for col in (date_col, group_col):
        if col is not None and col not in df.columns:
            print(f"ERROR: Missing column '{col}'", file=sys.stderr)
            sys.exit(2)

    if len(df) < 200:
        print(f"ERROR: Insufficient data: {len(df)} rows (need >= 200)", file=sys.stderr)
        sys.exit(3)

    feat = df[REQUIRED_FEATURES].to_numpy(dtype=np.float64, na_value=np.nan)
    if not np.isfinite(feat).all():
        bad = [
            c
            for c in REQUIRED_FEATURES
            if not np.isfinite(df[c].to_numpy(dtype=np.float64, na_value=np.nan)).all()
        ]
        print(f"ERROR: Non-finite / NaN values in features: {', '.join(bad)}", file=sys.stderr)
        sys.exit(2)

    labels = set(pd.unique(df[TARGET_COL].dropna()))
    if not labels.issubset({0.0, 1.0, 0, 1}):
        print(f"ERROR: Target '{TARGET_COL}' must be binary 0/1, got {sorted(labels)}", file=sys.stderr)
        sys.exit(2)


def compute_auc(y_true: FloatArray, y_score: FloatArray) -> float:
    """AUC via the Mann-Whitney statistic with correct tie handling (average
    ranks). No sklearn dependency."""
    n = len(y_true)
    if n == 0:
        return 0.5
    n_pos = int(np.sum(y_true == 1))
    n_neg = n - n_pos
    if n_pos == 0 or n_neg == 0:
        return 0.5

    order = np.argsort(y_score, kind="mergesort")
    sorted_scores = y_score[order]
    ranks = np.empty(n, dtype=np.float64)
    i = 0
    while i < n:
        j = i
        while j + 1 < n and sorted_scores[j + 1] == sorted_scores[i]:
            j += 1
        ranks[i : j + 1] = 0.5 * ((i + 1) + (j + 1))
        i = j + 1
    rank_of_pos = ranks[y_true[order] == 1]
    rank_sum = float(np.sum(rank_of_pos))
    return (rank_sum - n_pos * (n_pos + 1) / 2.0) / (n_pos * n_neg)


def walk_forward_folds(
    n_rows: int, n_folds: int, embargo: int
) -> list[tuple[npt.NDArray[np.int64], npt.NDArray[np.int64]]]:
    """Expanding-window forward-chaining folds. Assumes rows are already sorted
    chronologically. Each training window ends `embargo` rows before its
    validation window starts."""
    if n_folds < 2:
        raise ValueError("cv-folds must be >= 2")
    fold_size = n_rows // (n_folds + 1)
    if fold_size == 0:
        raise ValueError("Not enough rows for the requested number of folds")

    folds: list[tuple[npt.NDArray[np.int64], npt.NDArray[np.int64]]] = []
    for k in range(1, n_folds + 1):
        train_end = fold_size * k
        valid_start = train_end + embargo
        valid_end = valid_start + fold_size if k < n_folds else n_rows
        if valid_start >= n_rows:
            break
        train_idx = np.arange(0, train_end, dtype=np.int64)
        valid_idx = np.arange(valid_start, min(valid_end, n_rows), dtype=np.int64)
        if len(train_idx) and len(valid_idx):
            folds.append((train_idx, valid_idx))
    if not folds:
        raise ValueError("Could not construct any valid walk-forward folds")
    return folds


def purge_by_group(
    train_idx: npt.NDArray[np.int64],
    valid_idx: npt.NDArray[np.int64],
    groups: npt.NDArray[np.object_] | None,
) -> npt.NDArray[np.int64]:
    """Remove training rows whose group also appears in the validation fold, to
    kill cross-sectional leakage (e.g. the same ticker on both sides)."""
    if groups is None:
        return train_idx
    valid_groups = set(groups[valid_idx].tolist())
    keep = np.array([g not in valid_groups for g in groups[train_idx]], dtype=bool)
    return train_idx[keep]


def _xgb_params(scale_pos_weight: float) -> dict[str, object]:
    return {
        "objective": "binary:logistic",
        "eval_metric": "auc",
        "max_depth": 4,
        "learning_rate": 0.05,
        "subsample": 0.8,
        "colsample_bytree": 0.8,
        "min_child_weight": 5,
        "scale_pos_weight": scale_pos_weight,
        "seed": 42,
        "verbosity": 0,
    }


def _lgb_params(scale_pos_weight: float) -> dict[str, object]:
    return {
        "objective": "binary",
        "metric": "auc",
        "boosting_type": "gbdt",
        "num_leaves": 15,
        "max_depth": 4,
        "learning_rate": 0.05,
        # NOTE: bagging_fraction only takes effect when bagging_freq > 0.
        "bagging_fraction": 0.8,
        "bagging_freq": 1,
        "feature_fraction": 0.8,
        "min_child_samples": 20,
        "scale_pos_weight": scale_pos_weight,
        "seed": 42,
        "verbosity": -1,
    }


def _fold_auc_xgb(
    X: FloatArray, y: FloatArray, tr: npt.NDArray[np.int64], va: npt.NDArray[np.int64], spw: float
) -> tuple[float, int]:
    import xgboost as xgb

    dtrain = xgb.DMatrix(X[tr], label=y[tr], feature_names=REQUIRED_FEATURES)
    dvalid = xgb.DMatrix(X[va], label=y[va], feature_names=REQUIRED_FEATURES)
    booster = xgb.train(
        _xgb_params(spw),
        dtrain,
        num_boost_round=500,
        evals=[(dvalid, "valid")],
        early_stopping_rounds=20,
        verbose_eval=False,
    )
    best_iter = int(booster.best_iteration) + 1
    preds = booster.predict(dvalid, iteration_range=(0, best_iter))
    return compute_auc(y[va], np.asarray(preds, dtype=np.float64)), best_iter


def _fold_auc_lgb(
    X: FloatArray, y: FloatArray, tr: npt.NDArray[np.int64], va: npt.NDArray[np.int64], spw: float
) -> tuple[float, int]:
    import lightgbm as lgb

    dtrain = lgb.Dataset(X[tr], label=y[tr], feature_name=REQUIRED_FEATURES)
    dvalid = lgb.Dataset(X[va], label=y[va], reference=dtrain)
    booster = lgb.train(
        _lgb_params(spw),
        dtrain,
        num_boost_round=500,
        valid_sets=[dvalid],
        callbacks=[lgb.early_stopping(20, verbose=False), lgb.log_evaluation(0)],
    )
    best_iter = int(booster.best_iteration)
    preds = booster.predict(X[va], num_iteration=best_iter)
    return compute_auc(y[va], np.asarray(preds, dtype=np.float64)), best_iter


def train_final_xgb(
    X: FloatArray, y: FloatArray, spw: float, rounds: int
) -> tuple["xgb.Booster", dict[str, float]]:
    import xgboost as xgb

    dtrain = xgb.DMatrix(X, label=y, feature_names=REQUIRED_FEATURES)
    booster = xgb.train(_xgb_params(spw), dtrain, num_boost_round=rounds, verbose_eval=False)
    gain = booster.get_score(importance_type="gain")

    def _imp(v: float | list[float]) -> float:
        # get_score is typed as returning float | list[float]; gain importance
        # is scalar, so collapse the (never-taken) list branch defensively.
        return float(v[0]) if isinstance(v, list) else float(v)

    fi = {name: _imp(gain.get(name, 0.0)) for name in REQUIRED_FEATURES}
    return booster, fi


def train_final_lgb(
    X: FloatArray, y: FloatArray, spw: float, rounds: int
) -> tuple["lgb.Booster", dict[str, float]]:
    import lightgbm as lgb

    dtrain = lgb.Dataset(X, label=y, feature_name=REQUIRED_FEATURES)
    booster = lgb.train(_lgb_params(spw), dtrain, num_boost_round=rounds)
    gains = booster.feature_importance(importance_type="gain")
    fi = {name: float(gains[i]) for i, name in enumerate(REQUIRED_FEATURES)}
    return booster, fi


def run(config: TrainConfig) -> TrainResult:
    """Execute a full training run and RETURN the outcome. This is the seam:
    no sys.exit here, so tests can call run() in-process and assert on the
    returned TrainResult (and on the files it writes). `main` is a thin wrapper
    that translates the result into a process exit code.

    Note: validate_data still calls sys.exit for schema errors (codes 2/3);
    those paths are covered by the Tier 1 unit tests. run() owns the *gate*
    decision (codes 0/1).
    """
    os.makedirs(os.path.dirname(config.output_model) or ".", exist_ok=True)
    os.makedirs(os.path.dirname(config.output_metrics) or ".", exist_ok=True)

    df = pd.read_csv(config.input_csv)
    validate_data(df, config.date_col, config.group_col)

    # Chronological ordering so folds are strictly forward in time.
    if config.date_col is not None:
        df = df.sort_values(config.date_col, kind="mergesort").reset_index(drop=True)

    X: FloatArray = df[REQUIRED_FEATURES].to_numpy(dtype=np.float64)
    y: FloatArray = df[TARGET_COL].to_numpy(dtype=np.float64)
    groups: npt.NDArray[np.object_] | None = (
        df[config.group_col].to_numpy(dtype=object) if config.group_col is not None else None
    )

    n_pos = float(np.sum(y == 1))
    n_neg = float(np.sum(y == 0))
    spw = (n_neg / n_pos) if n_pos > 0 else 1.0

    print(
        f"Training {config.backend}: {len(df)} rows, {len(REQUIRED_FEATURES)} features, "
        f"folds={config.cv_folds}, pos={int(n_pos)}, neg={int(n_neg)}, scale_pos_weight={spw:.3f}"
    )

    folds = walk_forward_folds(len(df), config.cv_folds, config.embargo)
    fold_auc = _fold_auc_xgb if config.backend == "xgboost" else _fold_auc_lgb

    aucs: list[float] = []
    rounds: list[int] = []
    for i, (tr, va) in enumerate(folds, start=1):
        if config.purge_groups:
            tr = purge_by_group(tr, va, groups)
        if len(tr) < 100 or len(np.unique(y[tr])) < 2 or len(np.unique(y[va])) < 2:
            print(f"  fold {i}: skipped (insufficient/one-class data after purge)")
            continue
        auc, best_iter = fold_auc(X, y, tr, va, spw)
        aucs.append(auc)
        rounds.append(best_iter)
        print(f"  fold {i}: train={len(tr)} valid={len(va)} auc={auc:.4f} rounds={best_iter}")

    if not aucs:
        print("ERROR: No usable folds produced an AUC", file=sys.stderr)
        metrics = {
            "auc_mean": 0.0,
            "auc_std": 0.0,
            "auc_gate": 0.0,
            "auc_per_fold": [],
            "backend": config.backend,
            "training_samples": len(df),
            "cv_folds": config.cv_folds,
            "validation": "walk_forward",
            "purge_groups": bool(config.purge_groups),
            "embargo": config.embargo,
            "scale_pos_weight": spw,
            "final_rounds": 0,
            "feature_importance": {},
            "threshold_exceeded": False,
            "error": "no_usable_folds",
        }
        with open(config.output_metrics, "w") as f:
            json.dump(metrics, f, indent=2)
        return TrainResult(exit_code=1, metrics=metrics, model_written=False, metrics_path=config.output_metrics)

    mean_auc = float(np.mean(aucs))
    std_auc = float(np.std(aucs))
    gate_auc = mean_auc - std_auc  # penalise fragile, high-variance models
    best_rounds = int(np.median(rounds)) if rounds else 100
    print(f"OOS AUC: mean={mean_auc:.4f} std={std_auc:.4f} gate(mean-std)={gate_auc:.4f}")

    base_metrics: dict[str, object] = {
        "auc_mean": mean_auc,
        "auc_std": std_auc,
        "auc_gate": gate_auc,
        "auc_per_fold": aucs,
        "backend": config.backend,
        "training_samples": len(df),
        "cv_folds": config.cv_folds,
        "validation": "walk_forward",
        "purge_groups": bool(config.purge_groups),
        "embargo": config.embargo,
        "scale_pos_weight": spw,
        "final_rounds": best_rounds,
    }

    def write_metrics(extra: dict[str, object]) -> dict[str, object]:
        metrics = {**base_metrics, **extra}
        with open(config.output_metrics, "w") as f:
            json.dump(metrics, f, indent=2)
        return metrics

    # Gate on the out-of-sample lower bound, not a single lucky fold.
    if gate_auc <= config.min_auc:
        print(f"Gate AUC {gate_auc:.4f} <= {config.min_auc} - no model written")
        metrics = write_metrics({"feature_importance": {}, "threshold_exceeded": False})
        return TrainResult(
            exit_code=1,
            metrics=metrics,
            model_written=False,
            metrics_path=config.output_metrics,
        )

    # Refit on all data at the median best-round count from the folds.
    model: Model  # backend union; both boosters expose .save_model
    if config.backend == "xgboost":
        model, fi = train_final_xgb(X, y, spw, best_rounds)
    else:
        model, fi = train_final_lgb(X, y, spw, best_rounds)

    model.save_model(config.output_model)
    metrics = write_metrics({"feature_importance": fi, "threshold_exceeded": True})

    print(f"Model saved to {config.output_model}")
    print(f"Metrics saved to {config.output_metrics}")
    print(f"Top features: {dict(sorted(fi.items(), key=lambda kv: -kv[1])[:5])}")
    return TrainResult(
        exit_code=0,
        metrics=metrics,
        model_written=True,
        model_path=config.output_model,
        metrics_path=config.output_metrics,
    )


def _config_from_args(args: argparse.Namespace) -> TrainConfig:
    return TrainConfig(
        input_csv=args.input_csv,
        output_model=args.output_model,
        output_metrics=args.output_metrics,
        backend=args.backend,
        min_auc=args.min_auc,
        cv_folds=args.cv_folds,
        date_col=args.date_col,
        group_col=args.group_col,
        purge_groups=args.purge_groups,
        embargo=args.embargo,
    )


def main() -> None:
    result = run(_config_from_args(parse_args()))
    sys.exit(result.exit_code)


if __name__ == "__main__":
    # Scope warning suppression to the noisy libraries only.
    warnings.filterwarnings("ignore", category=UserWarning, module="xgboost")
    warnings.filterwarnings("ignore", category=UserWarning, module="lightgbm")
    main()
