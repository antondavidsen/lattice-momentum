"""
tests/unit/test_metrics.py
───────────────────────────
Unit tests for the Prometheus metrics module.

Covers:
- Counter increment behaviour for all defined metrics
- Gauge set/observe behaviour
- Label application on all metric types
- No naming collisions in exported metric names
"""

from __future__ import annotations

import pytest

from app import metrics as m


# ── Helpers ─────────────────────────────────────────────────────────────────────


def _counter_value(counter, **labels) -> float:
    """Return the current value of a Counter (labeled child)."""
    return counter.labels(**labels)._value.get()


class TestCounters:
    """Counters increment correctly with labels."""

    def test_screeners_total_increment(self) -> None:
        before = _counter_value(m.screeners_total, name="momentum", status="success")
        m.screeners_total.labels(name="momentum", status="success").inc()
        after = _counter_value(m.screeners_total, name="momentum", status="success")
        assert after - before == pytest.approx(1.0)

    def test_screeners_total_separate_labels(self) -> None:
        b_mom_success = _counter_value(m.screeners_total, name="momentum", status="success")
        b_ep_failure = _counter_value(m.screeners_total, name="episodic_pivot", status="failure")
        m.screeners_total.labels(name="momentum", status="success").inc()
        m.screeners_total.labels(name="episodic_pivot", status="failure").inc(2)
        a_mom_success = _counter_value(m.screeners_total, name="momentum", status="success")
        a_ep_failure = _counter_value(m.screeners_total, name="episodic_pivot", status="failure")
        assert a_mom_success - b_mom_success == pytest.approx(1.0)
        assert a_ep_failure - b_ep_failure == pytest.approx(2.0)

    def test_csv_saves_total_increment(self) -> None:
        before = _counter_value(m.csv_saves_total, screener="momentum")
        m.csv_saves_total.labels(screener="momentum").inc(3)
        after = _counter_value(m.csv_saves_total, screener="momentum")
        assert after - before == pytest.approx(3.0)

    def test_csv_saves_total_separate_screeners(self) -> None:
        b_mom = _counter_value(m.csv_saves_total, screener="momentum")
        b_ep = _counter_value(m.csv_saves_total, screener="episodic_pivot")
        m.csv_saves_total.labels(screener="momentum").inc()
        m.csv_saves_total.labels(screener="episodic_pivot").inc(5)
        a_mom = _counter_value(m.csv_saves_total, screener="momentum")
        a_ep = _counter_value(m.csv_saves_total, screener="episodic_pivot")
        assert a_mom - b_mom == pytest.approx(1.0)
        assert a_ep - b_ep == pytest.approx(5.0)

    def test_backend_posts_total(self) -> None:
        b_success = _counter_value(m.backend_posts_total, status="success")
        b_failure = _counter_value(m.backend_posts_total, status="failure")
        m.backend_posts_total.labels(status="success").inc()
        m.backend_posts_total.labels(status="failure").inc(2)
        a_success = _counter_value(m.backend_posts_total, status="success")
        a_failure = _counter_value(m.backend_posts_total, status="failure")
        assert a_success - b_success == pytest.approx(1.0)
        assert a_failure - b_failure == pytest.approx(2.0)

    def test_validation_failures(self) -> None:
        before = _counter_value(m.validation_failures, screener="momentum")
        m.validation_failures.labels(screener="momentum").inc(4)
        after = _counter_value(m.validation_failures, screener="momentum")
        assert after - before == pytest.approx(4.0)

    def test_fallback_usage(self) -> None:
        before = _counter_value(m.fallback_usage, screener="episodic_pivot")
        m.fallback_usage.labels(screener="episodic_pivot").inc()
        after = _counter_value(m.fallback_usage, screener="episodic_pivot")
        assert after - before == pytest.approx(1.0)

    def test_validation_errors_total(self) -> None:
        before = _counter_value(m.validation_errors_total, screener="momentum", field="close")
        m.validation_errors_total.labels(screener="momentum", field="close").inc(7)
        after = _counter_value(m.validation_errors_total, screener="momentum", field="close")
        assert after - before == pytest.approx(7.0)

    def test_validation_errors_total_separate_field(self) -> None:
        b_close = _counter_value(m.validation_errors_total, screener="momentum", field="close")
        b_volume = _counter_value(m.validation_errors_total, screener="momentum", field="volume")
        m.validation_errors_total.labels(screener="momentum", field="close").inc()
        m.validation_errors_total.labels(screener="momentum", field="volume").inc(3)
        a_close = _counter_value(m.validation_errors_total, screener="momentum", field="close")
        a_volume = _counter_value(m.validation_errors_total, screener="momentum", field="volume")
        assert a_close - b_close == pytest.approx(1.0)
        assert a_volume - b_volume == pytest.approx(3.0)

    def test_schema_version_changes(self) -> None:
        before = _counter_value(m.schema_version_changes, screener="momentum")
        m.schema_version_changes.labels(screener="momentum").inc(2)
        after = _counter_value(m.schema_version_changes, screener="momentum")
        assert after - before == pytest.approx(2.0)

    def test_market_data_fetches_total(self) -> None:
        b_ok = _counter_value(m.market_data_fetches_total, status="ok", provider="tradingview")
        b_err = _counter_value(m.market_data_fetches_total, status="error", provider="tradingview")
        m.market_data_fetches_total.labels(status="ok", provider="tradingview").inc()
        a_ok = _counter_value(m.market_data_fetches_total, status="ok", provider="tradingview")
        a_err = _counter_value(m.market_data_fetches_total, status="error", provider="tradingview")
        assert a_ok - b_ok == pytest.approx(1.0)
        assert a_err - b_err == pytest.approx(0.0)


class TestGauges:
    """Gauges can be set to a specific value."""

    def test_collection_duration_seconds(self) -> None:
        m.collection_duration_seconds.set(42.5)
        assert m.collection_duration_seconds._value.get() == pytest.approx(42.5)

    def test_collection_duration_seconds_reset(self) -> None:
        m.collection_duration_seconds.set(10.0)
        m.collection_duration_seconds.set(20.0)
        assert m.collection_duration_seconds._value.get() == pytest.approx(20.0)

    def test_schema_version(self) -> None:
        m.schema_version.labels(screener="momentum").set(2)
        assert m.schema_version.labels(screener="momentum")._value.get() == pytest.approx(2.0)

    def test_schema_version_separate_screeners(self) -> None:
        m.schema_version.labels(screener="momentum").set(2)
        m.schema_version.labels(screener="episodic_pivot").set(1)
        assert m.schema_version.labels(screener="momentum")._value.get() == pytest.approx(2.0)
        assert m.schema_version.labels(screener="episodic_pivot")._value.get() == pytest.approx(1.0)


class TestMetricNames:
    """Ensure no naming collisions and metric names are consistent."""

    def test_screeners_total_name(self) -> None:
        assert m.screeners_total._name == "tv_collector_screeners"

    def test_csv_saves_total_name(self) -> None:
        assert m.csv_saves_total._name == "tv_collector_csv_saves"

    def test_backend_posts_total_name(self) -> None:
        assert m.backend_posts_total._name == "tv_collector_backend_posts"

    def test_validation_failures_name(self) -> None:
        assert m.validation_failures._name == "tv_collector_validation_failures"

    def test_fallback_usage_name(self) -> None:
        assert m.fallback_usage._name == "tv_collector_fallback_usage"

    def test_validation_errors_total_name(self) -> None:
        assert m.validation_errors_total._name == "tv_collector_validation_errors"

    def test_collection_duration_seconds_name(self) -> None:
        assert m.collection_duration_seconds._name == "tv_collector_collection_duration_seconds"

    def test_schema_version_name(self) -> None:
        assert m.schema_version._name == "tv_collector_schema_version"

    def test_schema_version_changes_name(self) -> None:
        assert m.schema_version_changes._name == "tv_collector_schema_version_changes"

    def test_market_data_fetches_total_name(self) -> None:
        assert m.market_data_fetches_total._name == "market_data_fetches"

    def test_all_metric_names_unique(self) -> None:
        """All metric names must be unique to avoid Prometheus conflicts."""
        names = [
            m.screeners_total._name,
            m.csv_saves_total._name,
            m.backend_posts_total._name,
            m.validation_failures._name,
            m.fallback_usage._name,
            m.validation_errors_total._name,
            m.collection_duration_seconds._name,
            m.schema_version._name,
            m.schema_version_changes._name,
            m.market_data_fetches_total._name,
        ]
        assert len(names) == len(set(names)), f"Duplicate metric names: {names}"