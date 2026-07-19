# ── Stage 1: builder ──────────────────────────────────────────────────────────
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder

ARG TARGETOS=linux
ARG TARGETARCH=amd64

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build the nightly pipeline binary for the target architecture.
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" \
    -o /bin/nightly ./cmd/nightly


# ── Stage 2: runtime ──────────────────────────────────────────────────────────
# Alpine ships with busybox crond — no external downloads required.
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

# Create a non-root user for running the nightly binary.
# crond itself must run as root to write the crontab, but the cron job
# executes the binary as the 'nightly' user via the crontab user field.
RUN addgroup -S nightly && adduser -S -G nightly -u 65533 nightly

# Copy the compiled binaries.
COPY --from=builder /bin/nightly /nightly
RUN chmod +x /nightly


# Default schedule: Mon–Fri 21:00 UTC (17:00 ET = 1 h after US market close at 16:00 ET / 20:00 UTC).# NOTE: US market closes at 16:00 ET = 20:00 UTC during EDT (UTC-4).
#       Running at 21:00 UTC gives 1 h for data to propagate before the pipeline starts.
#       DO NOT use 18:00 UTC — that is 14:00 ET, two hours before market close.
ENV CRON_SCHEDULE="0 21 * * 1-5"

# Entrypoint script: builds the crontab from $CRON_SCHEDULE and runs supercronic.
COPY deployments/docker/cron-entrypoint.sh /cron-entrypoint.sh
RUN chmod +x /cron-entrypoint.sh

ENTRYPOINT ["/cron-entrypoint.sh"]
