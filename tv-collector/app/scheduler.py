"""
scheduler.py
────────────
Configures and owns the APScheduler BlockingScheduler.
The daily collection job is registered here; the actual work is
delegated to services/daily_job.py.

Schedule resolution (highest priority first):
  1. COLLECT_CRON  — full 5-field cron expression, e.g. "*/2 * * * *"
  2. COLLECT_TIME  — HH:MM daily trigger, e.g. "18:00"
"""

import structlog
from apscheduler.schedulers.blocking import BlockingScheduler
from apscheduler.triggers.cron import CronTrigger

from app.config import Config
from app.services.daily_job import run_daily_job

log = structlog.get_logger(__name__)


def build_scheduler(config: Config) -> BlockingScheduler:
    """
    Create and configure the BlockingScheduler.
    Call `.start()` on the returned instance to begin blocking execution.
    """
    scheduler = BlockingScheduler(timezone=config.timezone)

    trigger, schedule_desc = _build_trigger(config)

    # Bind config via closure so APScheduler can call the job with no arguments.
    def _job() -> None:
        try:
            run_daily_job(config)
        except Exception:
            log.exception("scheduler.job_unhandled_error")

    scheduler.add_job(
        _job,
        trigger=trigger,
        id="daily_collection",
        name="Daily TradingView collection",
        misfire_grace_time=60 * 30,
        coalesce=True,
    )

    log.info(
        "scheduler.configured",
        schedule=schedule_desc,
        timezone=config.timezone,
    )

    return scheduler


def _build_trigger(config: Config) -> tuple[CronTrigger, str]:
    """
    Return the appropriate CronTrigger and a human-readable description.

    Priority:
      1. COLLECT_CRON  (full cron expression — overrides everything)
      2. COLLECT_TIME  (HH:MM daily trigger)
    """
    if config.collect_cron.strip():
        trigger = CronTrigger.from_crontab(
            config.collect_cron.strip(),
            timezone=config.timezone,
        )
        return trigger, config.collect_cron.strip()

    hour, minute = _parse_collect_time(config.collect_time)
    trigger = CronTrigger(hour=hour, minute=minute, timezone=config.timezone)
    return trigger, f"{hour:02d}:{minute:02d}"


def _parse_collect_time(collect_time: str) -> tuple[int, int]:
    """Parse 'HH:MM' string into (hour, minute) integers."""
    try:
        hour_str, minute_str = collect_time.split(":")
        return int(hour_str), int(minute_str)
    except (ValueError, AttributeError) as exc:
        raise ValueError(
            f"COLLECT_TIME must be in HH:MM format, got '{collect_time}'"
        ) from exc

