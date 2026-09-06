// Command rolloutproof is RolloutProof's V1 CLI: a local, deterministic,
// offline verifier of one rollout plan (docs/vision.md §5).
//
// Usage:
//
//	rolloutproof verify [--format text|json|sarif] [--output <file>] <directory>
//	rolloutproof version
//
// <directory> follows internal/parser/rolloutplan's fixed convention:
// deployment.yaml, schema.yaml, rolloutplan.yaml, referenced migration
// files, and an optional contracts/ subdirectory of service metadata
// (internal/parser/contract). See examples/ for worked fixtures.
//
// # Output formats
//
// text (default) is internal/report.RenderText's human-readable form.
// json is internal/report.RenderJSON's stable, versioned machine-readable
// form (schemaVersion field). sarif is internal/report.RenderSARIF's
// SARIF 2.1.0 form, suitable for GitHub code-scanning upload
// (docs/CI.md). All three are deterministic byte-for-byte across
// repeated runs on identical input (docs/vision.md §9) and never differ
// in verdict — only in shape.
//
// --output writes to a file instead of stdout, so a CI step can produce
// a JSON/SARIF artifact alongside human-readable console output from a
// separate invocation.
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
//
// These exit codes hold regardless of --format: a CI step can select
// json/sarif purely for the artifact and still branch on $? exactly as
// it would with the default text output (docs/CI.md).
//
// --help (or -h) at the top level or on the verify subcommand prints
// this usage text and exits 0; it is not part of the verify exit-code
// contract above, which only governs a completed or attempted
// verification run.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/invariant"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/report"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/verify"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/version"
)

const usage = `rolloutproof verify [--format text|json|sarif] [--output <file>] <directory>
rolloutproof version

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
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return exitToolFailure
	}
	switch args[0] {
	case "--help", "-help", "-h", "help":
		fmt.Fprintln(stdout, usage)
		return exitSafe
	case "version":
		fmt.Fprintln(stdout, version.String())
		return exitSafe
	case "verify":
		return runVerify(args[1:], stdout, stderr)
	default:
		fmt.Fprintln(stderr, usage)
		return exitToolFailure
	}
}

func runVerify(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", "text", "output format: text, json, or sarif")
	output := fs.String("output", "", "write output to this file instead of stdout")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stdout, usage)
			return exitSafe
		}
		fmt.Fprintln(stderr, usage)
		return exitToolFailure
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, usage)
		return exitToolFailure
	}
	dir := fs.Arg(0)

	diags, err := verify.Run(dir)
	if err != nil {
		fmt.Fprintf(stderr, "rolloutproof: %v\n", err)
		return exitToolFailure
	}

	rendered, err := renderFormat(*format, diags)
	if err != nil {
		fmt.Fprintf(stderr, "rolloutproof: %v\n", err)
		return exitToolFailure
	}

	if *output != "" {
		if err := os.WriteFile(*output, rendered, 0o644); err != nil {
			fmt.Fprintf(stderr, "rolloutproof: writing output: %v\n", err)
			return exitToolFailure
		}
	} else if _, err := stdout.Write(rendered); err != nil {
		fmt.Fprintf(stderr, "rolloutproof: writing output: %v\n", err)
		return exitToolFailure
	}

	switch invariant.Aggregate(diags) {
	case ir.VerdictSafe:
		return exitSafe
	case ir.VerdictUnsafe:
		return exitUnsafe
	default:
		return exitUnknown
	}
}

// renderFormat dispatches to the requested internal/report renderer,
// always returning output ending in exactly one trailing newline
// regardless of format, so --output files and stdout both end cleanly.
func renderFormat(format string, diags []ir.Diagnostic) ([]byte, error) {
	switch format {
	case "text":
		return []byte(report.RenderText(diags) + "\n"), nil
	case "json":
		out, err := report.RenderJSON(diags)
		if err != nil {
			return nil, fmt.Errorf("rendering json: %w", err)
		}
		return append(out, '\n'), nil
	case "sarif":
		out, err := report.RenderSARIF(diags, version.Version)
		if err != nil {
			return nil, fmt.Errorf("rendering sarif: %w", err)
		}
		return append(out, '\n'), nil
	default:
		return nil, fmt.Errorf("unknown --format %q (expected text, json, or sarif)", format)
	}
}
