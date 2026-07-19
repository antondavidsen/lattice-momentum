#!/bin/sh
# cron-entrypoint.sh
# Writes a crontab from $CRON_SCHEDULE and runs busybox crond in the foreground.
# Logs are forwarded to /proc/1/fd/1 and /proc/1/fd/2 so they appear in
# `docker logs`.
#
# The cron job runs as ROOT (written to /etc/crontabs/root) so that the shell
# redirect >> /proc/1/fd/1 is permitted.  Running as the 'nightly' user
# (non-root) caused "Permission denied" because /proc/1/fd/1 is owned by the
# PID-1 process (crond, running as root) and unprivileged users cannot open
# another process's file descriptors.  For a Go binary that only makes
# outbound DB/HTTP calls this is an acceptable trade-off inside a container.

set -e

CRONTAB_DIR="/etc/crontabs"
mkdir -p "$CRONTAB_DIR"

# Write to root's crontab — no user field, no permission issue with
# /proc/1/fd/1.
echo "${CRON_SCHEDULE} /nightly >> /proc/1/fd/1 2>> /proc/1/fd/2" > "${CRONTAB_DIR}/root"

# Intraday ingestion — runs at 16:15 ET (20:15 UTC) Mon–Fri, after market close.
echo "15 20 * * 1-5 /intraday >> /proc/1/fd/1 2>> /proc/1/fd/2" >> "${CRONTAB_DIR}/root"

echo "Starting cron scheduler: ${CRON_SCHEDULE}"

# -f  run in foreground
# -d 8  log level (8 = debug)
exec crond -f -d 8
