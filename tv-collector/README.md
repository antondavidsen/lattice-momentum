# tv-collector

Python microservice that fetches TradingView screener data daily and forwards it to the Momentum AI Go backend.

## Responsibilities

- Run a scheduled daily job after US market close
- Fetch four TradingView screeners (Momentum, EP Candidates, Market Leaders, Sector)
- Persist raw CSV exports to `data/raw/`
- POST normalised payloads to the Go backend API

## Stack

| Concern | Library |
|---|---|
| Scheduling | APScheduler 3.x (BlockingScheduler + CronTrigger) |
| HTTP client | httpx |
| Config | python-dotenv + dataclass |
| Logging | structlog (JSON in prod, coloured console locally) |
| Validation | pydantic v2 |
| Packaging | Poetry |

## Local setup

```bash
# 1. Copy and fill in env vars
cp .env.example .env

# 2. Install dependencies
poetry install

# 3. Run
poetry run python -m app.main
```

## Docker

```bash
docker build -t tv-collector .
docker run --env-file .env tv-collector
```

## Project structure

```
app/
├── main.py           # Entry point — loads config, logging, starts scheduler
├── config.py         # Typed config loaded from env vars
├── scheduler.py      # APScheduler setup + run_daily_collection() job
├── logging_config.py # structlog initialisation
├── services/         # High-level orchestration (collection pipeline)
├── screeners/        # TradingView scraper implementations (one per screener)
├── clients/          # HTTP client for the Go backend API
└── models/           # Pydantic models for screener data
data/
└── raw/              # Raw CSV exports (volume-mounted in Docker)
```

## Adding a new screener

1. Create `app/screeners/my_screener.py` implementing a `fetch() -> list[dict]` function
2. Add it to the collection pipeline in `app/services/collection.py`
3. Add a corresponding Pydantic model in `app/models/`

## Environment variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `BACKEND_API_URL` | ✅ | — | Go backend base URL |
| `COLLECT_TIME` | | `18:00` | Daily run time (HH:MM, 24h) |
| `TIMEZONE` | | `America/New_York` | IANA timezone |
| `TV_BROWSER` | | `chromium` | Browser for TV scraping |
| `LOG_LEVEL` | | `INFO` | `DEBUG` / `INFO` / `WARNING` |
| `RAW_DATA_DIR` | | `./data/raw` | CSV output directory |

