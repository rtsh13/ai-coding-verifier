#!/usr/bin/env bash
#
# Build the offline sandbox images the verifier runs jobs in.
#
# The image is the language: `rust-sandbox` runs Rust submissions and
# `aicv/go-sandbox` runs Go submissions (these are the tags the CLI and Makefile
# default to). Each is a multi-stage build that vendors the selected dependencies
# so the container compiles and tests with the network switched off.
#
# Usage:
#   scripts/build-images.sh [rust|go|all]     # default: all
#
# Environment:
#   ENGINE   container engine to use (default: podman, else docker)
#
# Examples:
#   scripts/build-images.sh            # build both images
#   scripts/build-images.sh rust       # build only the Rust image
#   ENGINE=docker scripts/build-images.sh
set -euo pipefail

# Repo root, resolved relative to this script so it runs from anywhere.
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

RUST_TAG="rust-sandbox"
GO_TAG="aicv/go-sandbox:latest"

# Pick a container engine: honour $ENGINE, else prefer podman, else docker.
ENGINE="${ENGINE:-}"
if [ -z "$ENGINE" ]; then
  if command -v podman >/dev/null 2>&1; then
    ENGINE=podman
  elif command -v docker >/dev/null 2>&1; then
    ENGINE=docker
  else
    echo "build-images: no container engine found (need podman or docker)" >&2
    exit 1
  fi
fi

target="${1:-all}"
case "$target" in
  rust|go|all) ;;
  *)
    echo "build-images: unknown target '$target' (want: rust | go | all)" >&2
    exit 2
    ;;
esac

build_rust() {
  echo ">> building $RUST_TAG  (from images/rust)"
  "$ENGINE" build -t "$RUST_TAG" "$ROOT/images/rust"
}

build_go() {
  echo ">> building $GO_TAG  (from images/go)"
  "$ENGINE" build -t "$GO_TAG" "$ROOT/images/go"
}

echo "build-images: engine=$ENGINE  target=$target"
if [ "$target" = rust ] || [ "$target" = all ]; then build_rust; fi
if [ "$target" = go ]   || [ "$target" = all ]; then build_go; fi

echo
echo ">> images:"
"$ENGINE" images | grep -E 'rust-sandbox|go-sandbox' || true
