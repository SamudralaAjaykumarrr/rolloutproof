// Command rolloutproof is RolloutProof's V1 CLI: a local, deterministic,
// offline verifier of one rollout plan (docs/vision.md §5).
//
// Usage:
//
//	rolloutproof verify <directory>
//
// <directory> follows internal/parser/rolloutplan's fixed convention:
// deployment.yaml, schema.yaml, rolloutplan.yaml, referenced migration
// files, and an optional contracts/ subdirectory of service metadata
// (internal/parser/contract). See examples/ for worked fixtures.
//
// # Exit codes
//
//	0  SAFE    — every implemented invariant evaluated, none violated
//	1  UNSAFE  — at least one invariant is violated
//	2  UNKNOWN — no invariant is violated, but at least one could not be
//	             fully evaluated for lack of evidence (docs/vision.md §10)
//	>2 tool/input failure — a malformed file, a missing required file, or
//	             any other error that prevented verification from running
//	             at all. This is deliberately never conflated with UNKNOWN
//	             (an UNKNOWN verdict): UNKNOWN is a real, positive answer
//	             about the rollout; an exit code above 2 means no answer
//	             was reached at all.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/invariant"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/report"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/verify"
)

const usage = `rolloutproof verify <directory>

Verifies whether the rollout plan described by <directory> is safe.
See docs/vision.md and examples/ for the directory convention.`

const (
	exitSafe        = 0
	exitUnsafe      = 1
	exitUnknown     = 2
	exitToolFailure = 3
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 || args[0] != "verify" {
		fmt.Fprintln(stderr, usage)
		return exitToolFailure
	}
	diags, err := verify.Run(args[1])
	if err != nil {
		fmt.Fprintf(stderr, "rolloutproof: %v\n", err)
		return exitToolFailure
	}

	fmt.Fprintln(stdout, report.RenderText(diags))

	switch invariant.Aggregate(diags) {
	case ir.VerdictSafe:
		return exitSafe
	case ir.VerdictUnsafe:
		return exitUnsafe
	default:
		return exitUnknown
	}
}
