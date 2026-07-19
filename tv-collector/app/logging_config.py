
"""
logging_config.py
─────────────────
Configures structlog for structured JSON output.
Call `setup_logging()` once at process startup before any other code runs.
"""

import logging
import sys

import structlog


def _add_validation_context(logger, method_name, event_dict):
    """Add validation-specific context to log events."""
    event = event_dict.get("event", "")
    if event.startswith("validation."):
        event_dict.setdefault("component", "validation")
        event_dict.setdefault("domain", "application")
    return event_dict


def setup_logging(log_level: str = "INFO") -> None:
    """
    Wire structlog + stdlib logging so every log statement emits a
    single-line JSON object to stdout — compatible with Docker log drivers
    and log aggregators (Loki, CloudWatch, Datadog…).
    """
    level = getattr(logging, log_level.upper(), logging.INFO)

    # stdlib root logger — captures third-party library logs too
    logging.basicConfig(
        format="%(message)s",
        stream=sys.stdout,
        level=level,
    )

    def add_static_fields(logger, method_name, event_dict):
        """Add static context fields to every log event."""
        event_dict.update({"service_name": "lattice-tv-collector", "domain": "application"})
        return event_dict

    shared_processors: list[structlog.types.Processor] = [
        structlog.contextvars.merge_contextvars,
        structlog.stdlib.add_logger_name,
        structlog.stdlib.add_log_level,
        structlog.processors.TimeStamper(fmt="iso", utc=True),
        structlog.processors.StackInfoRenderer(),
        structlog.processors.ExceptionRenderer(),
        add_static_fields,
        _add_validation_context,
    ]

    structlog.configure(
        processors=[
            *shared_processors,
            structlog.stdlib.ProcessorFormatter.wrap_for_formatter,
        ],
        logger_factory=structlog.stdlib.LoggerFactory(),
        wrapper_class=structlog.stdlib.BoundLogger,
        cache_logger_on_first_use=True,
    )

    formatter = structlog.stdlib.ProcessorFormatter(
        # Final renderer: JSON in production, coloured console locally.
        processor=structlog.dev.ConsoleRenderer()
        if sys.stderr.isatty()
        else structlog.processors.JSONRenderer(),
        foreign_pre_chain=shared_processors,
    )

    handler = logging.StreamHandler(sys.stdout)
    handler.setFormatter(formatter)

    root_logger = logging.getLogger()
    root_logger.handlers = [handler]
    root_logger.setLevel(level)
