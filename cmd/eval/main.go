// Command eval is RolloutProof's scenario-corpus evaluation harness
// (docs/scenario-corpus.md): it runs the real verification pipeline
// (internal/verify) against every fixture under examples/, compares the
// actual overall verdict to the one its directory declares, and reports
// a pass/fail table plus totals and per-scenario runtime.
//
// This is a correctness-regression tool, not a benchmark and not a
// precision/recall report: with ~20 hand-built fixtures, computing
// "precision" or "recall" would be a statistically meaningless number
// dressed up as evidence (docs/FALSE_POSITIVES.md explains why this
// project reports known failure modes qualitatively instead). What this
// command reports is exactly two things per scenario — did the actual
// verdict match the expected one, and how long did it take — nothing
// inferred beyond that.
//
// Usage:
//
//	go run ./cmd/eval [examples-dir]
//
// examples-dir defaults to "examples" relative to the current directory.
// Exit code is 0 if every scenario's actual verdict matched its expected
// one, 1 otherwise — suitable for a CI regression gate
// (.github/workflows/ci.yml runs this on every push).
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/invariant"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/verify"
)

// expectedVerdictFor derives the expected overall verdict from a
// fixture's top-level category directory (examples/safe|unsafe|unknown/*)
// — the same convention docs/scenario-corpus.md's SAFE/UNSAFE/UNKNOWN
// categorization and this project's own directory layout already use,
// not a fact this command invents.
func expectedVerdictFor(category string) (ir.Verdict, bool) {
	switch category {
	case "safe":
		return ir.VerdictSafe, true
	case "unsafe":
		return ir.VerdictUnsafe, true
	case "unknown":
		return ir.VerdictUnknown, true
	default:
		return ir.VerdictSafe, false
	}
}

type result struct {
	category string
	name     string
	dir      string
	expected ir.Verdict
	actual   ir.Verdict
	pass     bool
	duration time.Duration
	err      error
	firings  []string // non-SAFE invariant IDs, for a quick "why" glance
}

func main() {
	root := "examples"
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	os.Exit(run(root, os.Stdout))
}

func run(root string, out io.Writer) int {
	results, err := evaluateAll(root)
	if err != nil {
		fmt.Fprintf(out, "eval: %v\n", err)
		return 1
	}

	fmt.Fprintf(out, "%-45s %-13s %-8s %-8s %-6s %s\n", "SCENARIO", "EXPECTED", "ACTUAL", "RESULT", "TIME", "FIRED")
	fmt.Fprintln(out, "--------------------------------------------------------------------------------------------")

	var passed, failed int
	var total time.Duration
	for _, r := range results {
		total += r.duration
		status := "PASS"
		if !r.pass {
			status = "FAIL"
			failed++
		} else {
			passed++
		}
		actual := r.actual.String()
		if r.err != nil {
			actual = "ERROR"
		}
		fmt.Fprintf(out, "%-45s %-13s %-8s %-8s %-6s %s\n",
			r.category+"/"+r.name, r.expected.String(), actual, status,
			r.duration.Round(time.Microsecond), fmt.Sprintf("%v", r.firings))
		if r.err != nil {
			fmt.Fprintf(out, "  error: %v\n", r.err)
		}
	}

	fmt.Fprintln(out, "--------------------------------------------------------------------------------------------")
	fmt.Fprintf(out, "%d scenarios: %d passed, %d failed, total runtime %s\n", len(results), passed, failed, total.Round(time.Microsecond))

	if failed > 0 {
		return 1
	}
	return 0
}

func evaluateAll(root string) ([]result, error) {
	categories, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", root, err)
	}

	var results []result
	for _, cat := range categories {
		if !cat.IsDir() {
			continue
		}
		expected, ok := expectedVerdictFor(cat.Name())
		if !ok {
			continue // a non-category directory under examples/, if any — not this harness's concern
		}
		catDir := filepath.Join(root, cat.Name())
		scenarios, err := os.ReadDir(catDir)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", catDir, err)
		}
		for _, sc := range scenarios {
			if !sc.IsDir() {
				continue
			}
			dir := filepath.Join(catDir, sc.Name())
			start := time.Now()
			diags, runErr := verify.Run(dir)
			duration := time.Since(start)

			r := result{category: cat.Name(), name: sc.Name(), dir: dir, expected: expected, duration: duration, err: runErr}
			if runErr == nil {
				r.actual = invariant.Aggregate(diags)
				r.pass = r.actual == expected
				r.firings = firingIDs(diags)
			}
			results = append(results, r)
		}
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].category != results[j].category {
			return results[i].category < results[j].category
		}
		return results[i].name < results[j].name
	})
	return results, nil
}

// firingIDs lists the invariant IDs that did not report SAFE, sorted —
// a quick "why" pointer alongside the pass/fail table, not a substitute
// for reading the full report (internal/report.RenderText/RenderJSON)
// for any scenario worth investigating.
func firingIDs(diags []ir.Diagnostic) []string {
	var ids []string
	for _, d := range diags {
		if d.Verdict != ir.VerdictSafe {
			ids = append(ids, d.InvariantID)
		}
	}
	sort.Strings(ids)
	return ids
}
