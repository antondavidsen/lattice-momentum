# `train_premonition.py` — Model Training Gate

A command-line program that decides **whether a predictive model is good enough
to ship**, and if so, produces it. It is a "sidecar": a small, single-purpose
tool meant to be run by an automated pipeline (cron job, CI step, orchestrator),
not a library you import.

This document assumes **no machine-learning background**. Every ML concept is
explained from first principles in plain engineering terms. If you have written
backend services and CI pipelines, you already have every mental model you need.

---

## 1. What problem does it solve?

We have historical records of "events" (in this codebase, trading setups). For
each past event we know:

- a set of **numeric features** describing the event (10 scores such as
  `volume_spike_score`, `earnings_quality_score`, ...), and
- an **outcome label**: `1.0` if the event turned out well, `0.0` if it didn't.

We want a function that, given the 10 features of a *new* event, outputs a
number expressing "how likely is this to turn out well". That function is the
**model**. This script builds that function from the historical data — but only
if it can prove the function is actually better than guessing.

Think of it as a **build step with a quality gate**: compile the model from
data, run it against a benchmark, and refuse to publish the artifact if it fails
the benchmark. Exactly like a CI job that won't cut a release if the test suite
is red.

---

## 2. The core concepts (zero ML assumed)

### 2.1 What is "the model"?

We use **gradient-boosted decision trees** (via one of two interchangeable
engines, XGBoost or LightGBM). You do **not** need the math. The intuition:

- A single **decision tree** is a nested `if/else`: *"if volume_spike_score >
  1.4 and trend_alignment_score > 0.2 then lean positive, else ..."*. It is a
  flowchart that ends in a score.
- One tree is weak. **Boosting** builds hundreds of small trees in sequence,
  where each new tree focuses on correcting the mistakes of the ones before it.
  The final prediction is the sum of all their small contributions.
- The result is just a deterministic scoring function: features in, a number
  between 0 and 1 out. No language, no text, no neural network — it is closer to
  a large auto-generated lookup/rule structure than to anything "AI-magical".

The trained model is serialized to a JSON file (`--output-model`). That file IS
the deliverable.

### 2.2 What is "AUC" and why is it the gate?

**AUC** (Area Under the ROC Curve) is a single number in `[0.0, 1.0]` that
measures **how well the model ranks positives above negatives**:

- `1.0` = perfect: every good event scored higher than every bad event.
- `0.5` = worthless: no better than flipping a coin.
- `< 0.5` = actively wrong (ranking is inverted).

Operational definition (this is literally how `compute_auc` works): take every
possible (positive, negative) pair; AUC is the fraction of pairs where the model
gave the positive the higher score. Ties count as half.

AUC is the **benchmark our quality gate checks**. If the model can't clear a
minimum AUC, the script exits non-zero and writes no model. That is the whole
point of the tool: **never ship an edgeless model.**

### 2.3 Why not just measure AUC on the training data?

Because a model can memorize the data it was trained on and score `1.0` on it
while being useless on anything new — the equivalent of a test that asserts
against hard-coded expected values copied from the current output. Meaningless.

So we measure AUC on data the model **did not see during training**. That is
called **out-of-sample (OOS)** evaluation. How we carve out that unseen data is
the most important design decision in the whole script (next section).

### 2.4 Validation, and the leakage trap

Standard practice is **cross-validation**: split the rows into chunks, train on
some, measure on the held-out chunk, repeat. The naive version picks the chunks
**randomly**. For time-ordered data (like markets) that is a serious bug called
**data leakage**:

- Random splitting lets the model train on rows from *the future* relative to
  the rows it is scored on. In production the future doesn't exist yet, so the
  measured AUC is inflated and lies to you.
- Correlated rows (same ticker, same day) can land on both sides of the split,
  so the model effectively "sees the answer".

This script avoids that with **walk-forward validation**:

1. Sort all rows by time (`--date-col`).
2. Fold 1: train on the earliest block, validate on the block right after it.
   Fold 2: train on a *larger* earliest block, validate on the next block. And
   so on — the training window only ever **expands forward in time**.
3. Training always ends strictly *before* validation begins. The invariant is:
   **never train on the future.**

Two extra safety knobs:

- `--embargo N`: drop `N` rows between the train and validation windows, so
  information doesn't bleed across the boundary.
- `--purge-groups` (with `--group-col`, e.g. `ticker`): remove any training row
  whose group also appears in the validation window. This kills
  **cross-sectional leakage** — the model memorizing "ticker AAPL tends to be
  positive" instead of learning a general, transferable rule.

---

## 3. What the script does, step by step

```
input CSV  ->  validate  ->  walk-forward CV  ->  gate decision  ->  outputs
```

1. **Parse arguments** (`parse_args`). Inputs, outputs, backend engine,
   minimum AUC, fold count, and the leakage-control knobs.
2. **Load & validate** (`validate_data`). Read the CSV; fail fast with a
   specific exit code if the schema is wrong (see the exit-code contract below).
3. **Prepare data**. Sort by date; build the numeric feature matrix `X` and the
   label vector `y`; compute `scale_pos_weight` to compensate if positives and
   negatives are imbalanced.
4. **Walk-forward cross-validation** (`walk_forward_folds` + `_fold_auc_*`). For
   each fold: optionally purge shared groups, train a model on the past,
   score it on the held-out future block, record that fold's OOS AUC.
5. **Aggregate**. Compute the mean and standard deviation of the per-fold AUCs.
   The gate compares **`mean - std`** to `--min-auc`. Subtracting the standard
   deviation deliberately penalizes models that are only good *on average* but
   wildly inconsistent across folds — we want a dependable edge, not a lucky one.
6. **Gate decision**:
   - If `mean - std <= --min-auc`: **reject.** Write a metrics file recording
     the failure and exit `1`. No model file is produced.
   - Otherwise: **accept.** Retrain one final model on *all* the data (using the
     median of the per-fold best iteration counts), save it, write the metrics
     file with `threshold_exceeded: true`, and exit `0`.

### Inputs

| Flag | Meaning |
|---|---|
| `--input-csv` | Labelled training data. Must contain the 10 feature columns + a `label` column. |
| `--output-model` | Where to write the trained model JSON (only written if the gate passes). |
| `--output-metrics` | Where to write the metrics JSON (always written, pass or fail). |
| `--backend` | `xgboost` or `lightgbm` — two interchangeable tree engines. |
| `--min-auc` | The quality bar the `mean - std` OOS AUC must clear. |
| `--cv-folds` | Number of walk-forward folds. |
| `--date-col` | Column used to order rows in time. |
| `--group-col` | Grouping column (e.g. `ticker`) for leakage purging. |
| `--purge-groups` | Enable cross-sectional leakage purging. |
| `--embargo` | Rows to drop between train and validation windows. |

### Outputs

- **Model JSON** (`--output-model`): the serialized scoring function. Written
  **only on success**.
- **Metrics JSON** (`--output-metrics`): always written. Contains the OOS AUC
  mean/std/gate, per-fold AUCs, feature importances, and a
  `threshold_exceeded` boolean.

### Exit-code contract

The exit code is the machine-readable result, so the calling pipeline can branch
on it (just like a Unix tool).

| Code | Meaning |
|---|---|
| `0` | Success — model cleared the gate and was written. |
| `1` | Gate not cleared — no model written (this is a *normal*, expected outcome, not a crash). |
| `2` | Bad input schema — missing feature/target column, NaN values, or non-binary labels. |
| `3` | Not enough data — fewer than 200 rows. |

---

## 4. How the code is organized (a testability map)

The script is deliberately split into **pure functions** (no I/O, no external
engines — same input always gives same output) and **orchestration/I/O** at the
edges. That separation is what makes it testable without a heavyweight setup.

| Function | Kind | Responsibility |
|---|---|---|
| `compute_auc` | pure | Rank-based AUC with correct tie handling. |
| `walk_forward_folds` | pure | Produce time-ordered (train, validation) index splits. |
| `purge_by_group` | pure | Drop training rows that share a group with validation. |
| `validate_data` | pure-ish | Enforce the input schema; exit with the right code. |
| `_fold_auc_xgb` / `_fold_auc_lgb` | engine | Train one fold and return its OOS AUC. |
| `train_final_xgb` / `train_final_lgb` | engine | Fit the final shippable model + feature importances. |
| `main` | orchestration | Wire it all together: load, validate, CV, gate, write. |

---

## 5. Test suite

The tests are built in **two tiers**, split by cost and dependencies. Tier 1
needs only `numpy`/`pandas`; Tier 2 needs a real tree engine installed and is
skipped automatically when one isn't present. All tests share synthetic data
produced by a single **factory fixture** (`make_dataset`) in
`tests/conftest.py`, which can generate three kinds of datasets on demand:

- **`signal`** — the label genuinely depends on the features (a real, learnable
  edge exists).
- **`noise`** — the label is independent of the features (no edge; AUC ~ 0.5).
- **`group_memorizable`** — each group (ticker) has a fixed fingerprint and a
  fixed label, but there is **no** general feature->label rule. A model can only
  "succeed" here by memorizing groups it has already seen. This is the dataset
  that exposes leakage.

### Tier 1 — unit tests (`tests/test_pure_functions.py`)

Fast, deterministic checks of the pure functions. They lock in the *logic* every
higher-level behavior depends on.

| Test | What it proves |
|---|---|
| `compute_auc` known values | Perfect ranking = 1.0, inverted = 0.0, a hand-checkable case = 0.75, and **all-ties = 0.5** (a naive implementation gets ties wrong). |
| `compute_auc` single class | With only one class present, AUC is undefined; the contract is to return `0.5`, never `NaN` (a `NaN` would silently poison the gate). |
| walk-forward "no future" | For every fold, `max(train index) < min(validation index)` — the invariant random splitting violates. |
| walk-forward embargo gap | An `--embargo N` really leaves exactly `N` rows between train and validation. |
| walk-forward expanding window | Training windows grow monotonically fold over fold. |
| walk-forward bad config | Nonsensical settings (too few folds/rows) raise instead of silently misbehaving. |
| purge removes shared groups | After purging, the train and validation group sets are disjoint. |
| purge no-op | With no group column, the training set is returned unchanged. |
| validate: missing feature | Exits with code `2`. |
| validate: too few rows | Exits with code `3`. |
| validate: NaN in features | Exits with code `2`. |
| validate: non-binary label | Exits with code `2`. |
| validate: clean frame | A well-formed dataset passes without error. |

### Tier 2 — business-case tests (integration)

End-to-end tests that run the trainer for real and assert on its **decisions and
side effects** (exit code, which files exist, what the metrics say). They require
a tree engine and are skipped when it isn't installed. Three cases, chosen
because they are the behaviors the business actually depends on:

| # | Business case | Dataset | Expected result |
|---|---|---|---|
| 1 | **Signal -> ship** | `signal` | Gate passes: exit `0`, model file **and** metrics file written, `threshold_exceeded: true`. Proves a real edge produces a real artifact. |
| 2 | **No signal -> refuse** | `noise` | Gate blocks: exit `1`, **no model file**, metrics written with `threshold_exceeded: false`. Proves the tool refuses to deploy an edgeless model — the most important safety property. |
| 3 | **Leakage integrity** | `group_memorizable` | With leakage allowed the model looks good; with `--purge-groups` the fake edge collapses and the gate blocks it. Proves the anti-leakage machinery actually works. |

> Note: Tier 2 files are added in the next step. This section documents their
> intended contract so the suite's coverage is understandable end to end.

---

## 6. Running it

```bash
# install once (any Python 3.11+)
pip install pytest numpy pandas xgboost lightgbm

# run the whole suite
cd mypy-basic
pytest -v

# run only the always-on unit tier (no tree engine needed)
pytest tests/test_pure_functions.py -v
```

Training the actual model on your own data:

```bash
python3 train_premonition.py \
    --input-csv data/training_data.csv \
    --output-model out/model.json \
    --output-metrics out/metrics.json \
    --backend xgboost \
    --min-auc 0.65 \
    --date-col event_date \
    --group-col ticker \
    --purge-groups \
    --embargo 3
```
