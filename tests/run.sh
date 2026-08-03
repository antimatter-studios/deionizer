#!/usr/bin/env bash
#
# One-command RED-GREEN correctness harness for the deionizer decoder.
#
# For every ground-truth PHP fixture x supported PHP version x encoding method:
#   encode (official ionCube trial encoder, in a linux/amd64 container)
#     -> decode (`deionizer decode`)
#       -> diff decoded-vs-original (artifact / lint / behavioral-equivalence).
#
# Encoding methods (the widened axis): plain, optimise-none, optimise-more,
# optimise-max, obfuscate-all, dynamic-keys. Behavior is graded name-independently
# (the decoded artifact is run as a whole program; its stdout is diffed against the
# original's — no hardcoded entry symbol), so obfuscation-renamed decodes are gradable.
#
# Produces:
#   tests/results/results.json               machine-readable matrix
#   tests/results/dashboard.md                dashboard (pass/fail + success rate)
#
# Usage:
#   ./tests/run.sh                               # full sweep (all methods x versions)
#   OBF=none VERSIONS=7.4 ./tests/run.sh         # quick smoke (plain, one version)
#   METHODS="plain optimise-max" ./tests/run.sh  # pick methods
#   FIXTURES="for_loop foreach_nested" ./tests/run.sh
#
# Env overrides: DECODER, ENCODER_DIR, ENCODER_IMAGE, VERSIONS, METHODS, OBF, FIXTURES.
# METHODS takes precedence over OBF; OBF is translated (none->plain, all->obfuscate-all).
set -euo pipefail

# Repo root = parent of this script's dir, regardless of caller CWD.
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

: "${DECODER:=/tmp/deionizer}"

command -v docker >/dev/null || { echo "run.sh: docker not found" >&2; exit 1; }
command -v go >/dev/null || { echo "run.sh: go not found" >&2; exit 1; }
[ -x "$DECODER" ] || { echo "run.sh: decoder not found at $DECODER (set DECODER=...)" >&2; exit 1; }

echo ">> deionizer correctness harness"
echo ">> repo:    $ROOT"
echo ">> decoder: $DECODER"

# Build the harness to a throwaway binary so `go build ./...` is never polluted
# and repeated runs are fast. Stdlib only; no module changes.
BIN="$(mktemp -t deionizer-harness.XXXXXX)"
trap 'rm -f "$BIN"' EXIT
go build -o "$BIN" ./tests/harness

exec "$BIN"
