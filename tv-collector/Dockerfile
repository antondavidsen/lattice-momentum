# ── Stage 1: dependency builder ───────────────────────────────────────────────
FROM python:3.11-slim AS builder

ENV POETRY_VERSION=1.8.3 \
    POETRY_NO_INTERACTION=1 \
    POETRY_VIRTUALENVS_IN_PROJECT=1 \
    POETRY_VIRTUALENVS_CREATE=1 \
    POETRY_CACHE_DIR=/tmp/poetry_cache

RUN pip install --no-cache-dir "poetry==${POETRY_VERSION}"

WORKDIR /app

# Copy the manifest first for layer caching.
COPY pyproject.toml poetry.lock* ./

# Regenerate the lock file to pick up any new dependencies added to
# pyproject.toml (e.g. fastapi, uvicorn) without changing pinned versions
# of packages that are already locked.  This is a no-op when the lock file
# is already up-to-date, so existing builds stay fast.
RUN poetry lock --no-update && \
    poetry install --only main --no-root && \
    rm -rf "$POETRY_CACHE_DIR"

# ── Stage 2: runtime ──────────────────────────────────────────────────────────
FROM python:3.11-slim AS runtime

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PATH="/app/.venv/bin:$PATH"

# wget is required by the Docker/compose healthcheck (GET /health).
# Use a single RUN layer to keep the image lean.
RUN apt-get update \
    && apt-get install -y --no-install-recommends wget \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Copy the virtualenv from the builder stage.
COPY --from=builder /app/.venv /app/.venv

# Copy application source and utility scripts.
COPY app/ ./app/
COPY scripts/ ./scripts/

# Create the data directory expected by the service.
RUN mkdir -p ./data/raw

# Non-root user for security.
RUN adduser --disabled-password --gecos "" appuser \
    && chown -R appuser:appuser /app
USER appuser

ENTRYPOINT ["python", "-m", "app.main"]
