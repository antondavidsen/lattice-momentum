# ── Stage 1: builder ──────────────────────────────────────────────────────────
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder

ARG TARGETOS=linux
ARG TARGETARCH=amd64

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build the backfill binary for the target architecture.
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" \
    -o /bin/backfill ./cmd/backfill

# ── Stage 2: runtime ──────────────────────────────────────────────────────────
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /bin/backfill /backfill

# Default: backfill all symbols for 2 years.
# Override at runtime:  docker compose run backfill -ticker AAPL -years 3
ENTRYPOINT ["/backfill"]
CMD ["-years", "2"]


