"""Tier 1: fast, deterministic unit tests for the pure functions.

These need only numpy/pandas -- no xgboost/lightgbm -- so they run everywhere
and in milliseconds. They lock in the *logic* the business cases depend on:
how AUC is scored, how the time-split is cut, how leakage is purged, and how
input validation fails.

Go -> pytest translations you'll see below:
- `@pytest.mark.parametrize` == Go table-driven tests: the `[]struct{...}`
  slice + `for _, tc := range cases` loop, except pytest reports each row as a
  separately named test case.
- plain `assert x == y` replaces `if got != want { t.Errorf(...) }`. pytest
  rewrites the assert so the failure message shows both sides automatically.
- `pytest.raises(SystemExit)` == asserting a function exits/errors; we then
  inspect `.value.code`, like checking the returned error value.
- `pytest.approx` == a float tolerance compare (no reflect.DeepEqual games).
"""
from __future__ import annotations

import numpy as np
import pytest

import train_premonition as tp


# --------------------------------------------------------------------------- #
# compute_auc: the metric everything else is gated on
# --------------------------------------------------------------------------- #
@pytest.mark.parametrize(
    "y_true, y_score, expected",
    [
        # perfect ranking: every positive scored above every negative -> 1.0
        ([0.0, 0.0, 1.0, 1.0], [0.1, 0.2, 0.8, 0.9], 1.0),
        # perfectly inverted ranking -> 0.0
        ([0.0, 0.0, 1.0, 1.0], [0.9, 0.8, 0.2, 0.1], 0.0),
        # a classic hand-checkable case (3 of 4 pos/neg pairs concordant)
        ([0.0, 0.0, 1.0, 1.0], [0.1, 0.4, 0.35, 0.8], 0.75),
        # all identical scores -> pure ties -> 0.5 (this is why tie handling
        # matters; a naive rank-sum would report 1.0 or 0.0 here)
        ([0.0, 1.0, 0.0, 1.0], [0.5, 0.5, 0.5, 0.5], 0.5),
    ],
)
def test_compute_auc_known_values(y_true, y_score, expected):
    auc = tp.compute_auc(np.array(y_true), np.array(y_score))
    assert auc == pytest.approx(expected)


@pytest.mark.parametrize("labels", [[1.0, 1.0, 1.0], [0.0, 0.0, 0.0]])
def test_compute_auc_single_class_is_undefined_half(labels):
    # AUC is undefined with only one class present; the contract is to return
    # 0.5 rather than divide by zero. Locking this in prevents a NaN silently
    # poisoning the gate.
    y = np.array(labels)
    scores = np.linspace(0, 1, len(labels))
    assert tp.compute_auc(y, scores) == 0.5


# --------------------------------------------------------------------------- #
# walk_forward_folds: the anti-leakage time split
# --------------------------------------------------------------------------- #
def test_walk_forward_never_trains_on_the_future():
    # THE core property: for every fold, every training row is strictly before
    # every validation row. This is the invariant random k-fold violates.
    folds = tp.walk_forward_folds(n_rows=120, n_folds=5, embargo=0)
    assert len(folds) >= 1
    for train_idx, valid_idx in folds:
        assert train_idx.max() < valid_idx.min()


def test_walk_forward_embargo_creates_the_expected_gap():
    embargo = 5
    folds = tp.walk_forward_folds(n_rows=120, n_folds=4, embargo=embargo)
    for train_idx, valid_idx in folds:
        gap = int(valid_idx.min()) - int(train_idx.max()) - 1
        assert gap == embargo


def test_walk_forward_training_window_expands():
    folds = tp.walk_forward_folds(n_rows=180, n_folds=5, embargo=0)
    train_sizes = [len(train_idx) for train_idx, _ in folds]
    assert train_sizes == sorted(train_sizes)   # monotonically non-decreasing
    assert len(set(train_sizes)) > 1            # and it actually grows


@pytest.mark.parametrize(
    "n_rows, n_folds",
    [
        (100, 1),   # too few folds
        (5, 5),     # too few rows for the fold count
    ],
)
def test_walk_forward_rejects_bad_configuration(n_rows, n_folds):
    with pytest.raises(ValueError):
        tp.walk_forward_folds(n_rows=n_rows, n_folds=n_folds, embargo=0)


# --------------------------------------------------------------------------- #
# purge_by_group: cross-sectional leakage guard
# --------------------------------------------------------------------------- #
def test_purge_removes_shared_groups():
    groups = np.array(["AAPL", "MSFT", "AAPL", "NVDA", "MSFT", "TSLA"], dtype=object)
    train_idx = np.array([0, 1, 2], dtype=np.int64)   # AAPL, MSFT, AAPL
    valid_idx = np.array([3, 4, 5], dtype=np.int64)   # NVDA, MSFT, TSLA
    kept = tp.purge_by_group(train_idx, valid_idx, groups)
    train_groups = set(groups[kept])
    valid_groups = set(groups[valid_idx])
    # MSFT was in both -> must be gone from training; disjoint afterwards.
    assert train_groups.isdisjoint(valid_groups)
    assert "MSFT" not in train_groups


def test_purge_is_a_noop_without_groups():
    train_idx = np.array([0, 1, 2], dtype=np.int64)
    valid_idx = np.array([3, 4], dtype=np.int64)
    kept = tp.purge_by_group(train_idx, valid_idx, None)
    assert np.array_equal(kept, train_idx)


# --------------------------------------------------------------------------- #
# validate_data: the schema gate and its exit-code contract
# --------------------------------------------------------------------------- #
def test_validate_data_accepts_a_clean_frame(make_dataset):
    df = make_dataset(mode="signal", n_rows=300)
    # Should NOT raise. (No assertion needed; a raised SystemExit fails here.)
    tp.validate_data(df, date_col="event_date", group_col="ticker")


def test_validate_data_rejects_missing_feature(make_dataset):
    df = make_dataset(mode="signal", n_rows=300).drop(columns=["options_flow_score"])
    with pytest.raises(SystemExit) as exc:
        tp.validate_data(df, None, None)
    assert exc.value.code == 2


def test_validate_data_rejects_too_few_rows(make_dataset):
    df = make_dataset(mode="signal", n_rows=150)
    with pytest.raises(SystemExit) as exc:
        tp.validate_data(df, None, None)
    assert exc.value.code == 3


def test_validate_data_rejects_nan_features(make_dataset):
    df = make_dataset(mode="signal", n_rows=300)
    df.loc[0, "volume_spike_score"] = np.nan
    with pytest.raises(SystemExit) as exc:
        tp.validate_data(df, None, None)
    assert exc.value.code == 2


def test_validate_data_rejects_non_binary_label(make_dataset):
    df = make_dataset(mode="signal", n_rows=300)
    df.loc[0, tp.TARGET_COL] = 2.0
    with pytest.raises(SystemExit) as exc:
        tp.validate_data(df, None, None)
    assert exc.value.code == 2
