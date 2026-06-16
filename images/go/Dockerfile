# syntax=docker/dockerfile:1.7
#
# Go sandbox image for the AI Coding Verifier.
#
# Stages:
#   1. builder — populates GOMODCACHE with K=42 baked dependencies
#                (see ADR-001 for selection methodology)
#   2. runtime — minimal image with toolchain + module cache, non-root user,
#                no network access expected at job execution time
#
# Build:  docker build -t aicv/go-sandbox:latest images/go
# Run:    docker run --rm --network none -e GOPROXY=off \
#             -v $PWD/job:/work aicv/go-sandbox:latest \
#             sh -c "cd /work && go build ./..."

# ─────────────────────────────────────────────────────────────
# Stage 1 — builder: download baked dependencies into module cache
# ─────────────────────────────────────────────────────────────
FROM golang:1.26-alpine AS builder

# git is required by `go mod download` for VCS-based fetches
# (some pseudo-versioned packages, e.g. golang.org/x/exp)
RUN apk add --no-cache git ca-certificates

ENV GOMODCACHE=/go/pkg/mod \
    GOCACHE=/root/.cache/go-build \
    CGO_ENABLED=0

WORKDIR /baked

# Copy only go.mod first (cache layer key)
COPY go.mod ./

# Materialise the module cache. This is the slow step.
# It runs ONCE at image build time and the result is baked into the layer.
RUN go mod download -x

# Sanity check: ensure GOMODCACHE is non-empty
RUN test "$(ls -A /go/pkg/mod/cache/download)" || \
    (echo "ERROR: module cache empty after go mod download" && exit 1)


# ─────────────────────────────────────────────────────────────
# Stage 2 — runtime: minimal sandbox image
# ─────────────────────────────────────────────────────────────
FROM golang:1.26-alpine AS runtime

# Runtime needs: git (for any submission that uses go modules with VCS refs,
# though offline mode mostly avoids this), ca-certificates (TLS roots in case
# of debugging), and tini for proper PID 1 signal handling
RUN apk add --no-cache ca-certificates tini && \
    rm -rf /var/cache/apk/*

# Non-root execution user
# UID/GID 1000 is conventional; sandboxed jobs run as this user.
RUN addgroup -g 1000 -S sandbox && \
    adduser  -u 1000 -S sandbox -G sandbox -h /home/sandbox && \
    mkdir -p /home/sandbox /tmp/exec && \
    chown -R sandbox:sandbox /home/sandbox /tmp/exec

# Copy the populated module cache from the builder stage.
# This is the entire point of multi-stage: the builder's intermediate
# layers (git, transient download state) don't bloat the runtime image.
COPY --from=builder --chown=sandbox:sandbox /go/pkg/mod /go/pkg/mod

# Bake go.mod into the image for reference & reproducibility.
# Stored at /baked/go.mod — NOT at /work, so submitted code doesn't see it.
COPY --chown=sandbox:sandbox go.mod /baked/go.mod

# Enforce offline execution at the image level.
# GOPROXY=off  → no proxy fetches
# GOFLAGS=-mod=mod → still allow go.mod resolution against the cache
# CGO_ENABLED=0 → no host C library coupling, fully static builds
ENV GOPROXY=off \
    GOSUMDB=off \
    GOFLAGS=-mod=mod \
    GOMODCACHE=/go/pkg/mod \
    GOCACHE=/home/sandbox/.cache/go-build \
    CGO_ENABLED=0 \
    HOME=/home/sandbox

# Per-job working directory; the orchestrator will bind-mount
# /tmp/exec/<job-id> here and chdir before invoking go build.
WORKDIR /tmp/exec

USER sandbox

# tini reaps zombie processes left by misbehaving submissions
ENTRYPOINT ["/sbin/tini", "--"]

# Default to a shell so the orchestrator can `docker exec` arbitrary commands;
# the container itself just idles.
CMD ["sh", "-c", "while true; do sleep 3600; done"]