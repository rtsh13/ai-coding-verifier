# Rust sandbox image for the AI Coding Verifier.
#
# GENERATED infrastructure — mirrors Go sandbox pattern (images/go/Dockerfile).
# Baked dependencies selected via repo-prevalence ≥10% methodology (see ADR-001).
# Offline isolation via cargo vendor + .cargo/config.toml source replacement.
#
# Build:   podman build -t rust-sandbox images/rust/
# Test:    podman run --rm --network none rust-sandbox cargo build --offline 2>&1

# ── Stage 1: Vendor layer (cache-friendly) ──────────────────
FROM rust:1.96-alpine AS vendor

RUN apk add --no-cache musl-dev openssl-dev pkgconfig openssl-libs-static
WORKDIR /build

# Copy manifests + vendor config first (cache layer — rarely changes)
COPY Cargo.toml Cargo.lock ./
COPY .cargo/config.toml .cargo/config.toml
COPY src/lib.rs src/lib.rs
COPY vendor/ vendor/

# Pre-compile all vendored dependencies (release mode for smaller cache)
# This bakes compiled artifacts into the image layer.
RUN cargo build --offline --release 2>&1 \
    && cargo build --offline 2>&1

# ── Stage 2: Runtime sandbox ────────────────────────────────
FROM rust:1.96-alpine

RUN apk add --no-cache musl-dev openssl-dev pkgconfig openssl-libs-static

# Non-root sandbox user (UID 1000) — matches Go sandbox pattern
RUN adduser -D -u 1000 sandbox

# Baked dependency sources
WORKDIR /sandbox
COPY --from=vendor /build/Cargo.toml /build/Cargo.lock ./
COPY --from=vendor /build/.cargo/config.toml .cargo/config.toml
COPY --from=vendor /build/src/lib.rs src/lib.rs
COPY --from=vendor /build/vendor/ vendor/

# Pre-compiled dependency artifacts (both debug + release)
COPY --from=vendor /build/target/ target/

# Job execution directory — submissions land here
RUN mkdir -p /tmp/exec && chown sandbox:sandbox /tmp/exec

# Offline isolation: no network, no registry access
ENV CARGO_NET_OFFLINE=true

# Ensure cargo uses vendored sources
ENV CARGO_HOME=/sandbox/.cargo

# Own everything as sandbox user
RUN chown -R sandbox:sandbox /sandbox

USER sandbox
WORKDIR /tmp/exec

# Default: verify the sandbox works
CMD ["rustc", "--version"]