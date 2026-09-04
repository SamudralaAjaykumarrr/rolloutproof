#!/usr/bin/env bash
# demo.sh — a reproducible walkthrough of RolloutProof's real engine.
#
# This runs the actual compiled CLI against this repository's own
# examples/ fixtures — every line of output below is produced live by
# `go build` + `rolloutproof verify`, not pre-recorded or hand-written.
# Run it yourself:
#
#   ./scripts/demo.sh
#
# It exits 0 regardless of the individual verdicts shown (SAFE/UNSAFE/
# UNKNOWN are all *expected*, correct results being demonstrated, not
# failures of the demo itself) — unless the build fails or a verdict
# doesn't match what this script asserts it should be, which would mean
# something is actually broken.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

BIN="$(mktemp -d)/rolloutproof"
trap 'rm -rf "$(dirname "$BIN")"' EXIT

section() {
	echo
	echo "================================================================"
	echo "  $1"
	echo "================================================================"
}

run_and_check() {
	local label="$1" dir="$2" want_exit="$3"
	section "$label"
	echo "\$ rolloutproof verify $dir"
	echo
	set +e
	"$BIN" verify "$dir"
	got_exit=$?
	set -e
	echo
	echo "(exit code: $got_exit)"
	if [ "$got_exit" -ne "$want_exit" ]; then
		echo "DEMO FAILED: expected exit $want_exit for $dir, got $got_exit" >&2
		exit 1
	fi
}

section "Building rolloutproof from source"
echo "\$ go build -o rolloutproof ./cmd/rolloutproof"
go build -o "$BIN" ./cmd/rolloutproof
echo "built."
"$BIN" version

run_and_check "SAFE — a purely additive migration" \
	"examples/safe/additive-column" 0

run_and_check "UNSAFE — the flagship scenario (destructive migration, mixed-version coexistence)" \
	"examples/unsafe/drop-column-before-drain" 1

run_and_check "UNKNOWN — missing contract metadata, never silently treated as SAFE" \
	"examples/unknown/missing-service-metadata" 2

run_and_check "CROSS-LAYER — migration commits irreversibly, then a rollback is attempted" \
	"examples/unsafe/rollback-after-irreversible-drop" 1

section "Machine-readable output (JSON)"
echo "\$ rolloutproof verify --format json examples/unsafe/drop-column-before-drain"
echo
set +e
"$BIN" verify --format json examples/unsafe/drop-column-before-drain
set -e

section "Scenario corpus evaluation (cmd/eval)"
echo "\$ go run ./cmd/eval"
echo
go run ./cmd/eval

section "Demo complete"
echo "Every result above came from the real engine (internal/verify) running"
echo "against real on-disk fixtures under examples/ — see docs/scenario-corpus.md"
echo "for what each one demonstrates, and docs/FALSE_POSITIVES.md for exactly"
echo "what a SAFE/UNSAFE/UNKNOWN verdict does and does not prove."
