# tv-collector Test Suite

Unit and integration tests for the `tv-collector` service, located in `tv-collector/app/`.

## Running the Tests

All tests are run via `pytest` from the `tv-collector/` directory, using the Poetry-managed virtual environment:

```bash
cd tv-collector/
poetry run pytest
```

### Common options

| Command | Description |
|---|---|
| `poetry run pytest` | Run all tests |
| `poetry run pytest -m "not integration"` | Run only unit tests (fast) |
| `poetry run pytest -m integration` | Run only integration tests |
| `poetry run pytest tests/unit/ -v` | Run unit tests with verbose output |
| `poetry run pytest tests/unit/test_momentum.py -v` | Run a single test file |
| `poetry run pytest -k "test_empty"` | Run tests matching a keyword |
| `poetry run pytest --cov=app --cov-report=term-missing` | Run with coverage report |

## Test Structure

```
tests/
├── conftest.py              # Shared fixtures (factory functions, mocks, temp dirs)
├── pytest.ini               # pytest configuration
├── README.md                # This file
├── unit/
│   ├── test_stage1_filter.py    # Stage 1 filter screener
│   ├── test_momentum.py         # Momentum screener
│   ├── test_market_leaders.py   # Market leaders screener
│   ├── test_episodic_pivot.py   # Episodic pivot screener
│   ├── test_audit_writer.py     # Audit writer
│   ├── test_validation.py       # Validation service
│   ├── test_daily_job.py        # Daily job orchestration
│   ├── test_storage.py          # Storage service
│   ├── test_config.py           # Configuration loading
│   ├── test_metrics.py          # Prometheus metrics
│   └── test_clients.py          # HTTP clients (Backend, TradingView)
└── integration/
    └── test_screener_pipeline.py  # Full pipeline integration tests
```

## Coverage Goals

Target ≥80% line coverage across the `app/` package.

## Testing Philosophy

1. **Unit tests** — Fast, deterministic, no I/O. All external dependencies mocked.
2. **Integration tests** — Real orchestration logic with mocked network calls.
   Marked with `@pytest.mark.integration`.
3. **Pure asserts** — No `assertTrue`/`assertEqual` — plain Python `assert`.
4. **Parametrized tests** — `@pytest.mark.parametrize` for table-driven tests.
5. **Descriptive names** — `test_<scenario>_<expected_result>`.