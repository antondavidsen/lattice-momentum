# ── Stage 1: builder ──────────────────────────────────────────────────────────
# BUILDPLATFORM = host platform (used to run the compiler)
# TARGETPLATFORM / TARGETOS / TARGETARCH = target platform for the binary
# Using these ARGs lets Docker BuildKit produce a native binary on every
# architecture (amd64 on Intel/Linux, arm64 on Apple Silicon / AWS Graviton)
# without cross-compilation overhead.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder

ARG TARGETOS=linux
ARG TARGETARCH=amd64

# Install git + CA certs (needed for go mod download over HTTPS)
RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /build

# Cache dependency downloads separately from source compilation.
COPY go.mod go.sum ./
RUN go mod download

# Copy full source and build a statically linked binary for the target arch.
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" \
    -o /bin/api ./cmd/api

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" \
    -o /bin/backfill ./cmd/backfill

# ── Stage 2: runtime ──────────────────────────────────────────────────────────
# alpine instead of scratch so Docker healthchecks (wget) work.
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata wget

# Copy the compiled binaries.
COPY --from=builder /bin/api /api
COPY --from=builder /bin/backfill /backfill

# Non-root user
RUN adduser -D -u 65534 nobody 2>/dev/null || true
USER 65534

EXPOSE 8080

ENTRYPOINT ["/api"]
