// Package report renders ir.Diagnostic results as deterministic,
// human-readable text (docs/architecture.md §11). It owns exactly one
// rendering function per output format — text today, JSON/SARIF planned
// (docs/vision.md §7) — so that adding a format never touches invariant
// logic (docs/adr/0006): every field in the rendered output traces back
// to a Diagnostic field, never to a hand-written string keyed on a
// scenario.
package report

import (
	"fmt"
	"strings"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/invariant"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
)

// RenderText renders the overall rollout verdict and every diagnostic
// invariant.Evaluate produced. SAFE diagnostics are summarized, not
// spelled out line by line, since they carry no evidence to show; UNSAFE
// and UNKNOWN diagnostics are rendered in full.
func RenderText(diags []ir.Diagnostic) string {
	overall := invariant.Aggregate(diags)

	var safe, unsafe, unknown int
	for _, d := range diags {
		switch d.Verdict {
		case ir.VerdictSafe:
			safe++
		case ir.VerdictUnsafe:
			unsafe++
		default:
			unknown++
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "ROLLOUT: %s\n", overall)
	fmt.Fprintf(&b, "\n%d invariant(s) evaluated: %d safe, %d unsafe, %d unknown\n", len(diags), safe, unsafe, unknown)

	for _, d := range diags {
		if d.Verdict == ir.VerdictSafe {
			continue
		}
		b.WriteString("\n")
		b.WriteString(strings.Repeat("-", 60))
		b.WriteString("\n")
		renderDiagnostic(&b, d)
	}

	return b.String()
}

func renderDiagnostic(b *strings.Builder, d ir.Diagnostic) {
	if d.Advisory {
		fmt.Fprintf(b, "%s  %s (advisory — does not block ROLLOUT verdict)\n\n", d.InvariantID, d.Verdict)
	} else {
		fmt.Fprintf(b, "%s  %s\n\n", d.InvariantID, d.Verdict)
	}
	b.WriteString(d.Summary)
	b.WriteString("\n")

	if len(d.Evidence) > 0 {
		b.WriteString("\nEvidence:\n")
		for _, e := range d.Evidence {
			if e.Artifact != "" {
				fmt.Fprintf(b, "- %s: %s\n", e.Artifact, e.Description)
			} else {
				fmt.Fprintf(b, "- %s\n", e.Description)
			}
		}
	}

	if d.Counterexample != nil {
		b.WriteString("\nCounterexample:\n")
		for i, ev := range d.Counterexample.Path {
			fmt.Fprintf(b, "%d. %s\n", i+1, ev.Detail)
		}
		if d.Counterexample.Outcome != "" {
			fmt.Fprintf(b, "%d. runtime failure: %s\n", len(d.Counterexample.Path)+1, d.Counterexample.Outcome)
		}
	}

	if d.Verdict == ir.VerdictUnsafe {
		fmt.Fprintf(b, "\nRollback: %s\n", d.RollbackVerdict)
	}

	if d.Counterexample != nil && len(d.Counterexample.RecommendedSequence) > 0 {
		b.WriteString("\nRecommended sequence:\n")
		for i, step := range d.Counterexample.RecommendedSequence {
			fmt.Fprintf(b, "%d. %s\n", i+1, step)
		}
	}

	if len(d.MissingEvidence) > 0 {
		b.WriteString("\nMissing evidence:\n")
		for _, g := range d.MissingEvidence {
			fmt.Fprintf(b, "- %s: %s\n", g.Field, g.Reason)
		}
	}
}

// Summary renders a one-line result, e.g. "SAFE — 2 invariant(s)
// evaluated, 0 violated, 0 unknown" — the style used throughout
// docs/scenario-corpus.md's "Expected diagnostic" fields.
func Summary(diags []ir.Diagnostic) string {
	overall := invariant.Aggregate(diags)
	var unsafe, unknown int
	for _, d := range diags {
		switch d.Verdict {
		case ir.VerdictUnsafe:
			unsafe++
		case ir.VerdictUnknown:
			unknown++
		}
	}
	return fmt.Sprintf("%s — %d invariant(s) evaluated, %d violated, %d unknown", overall, len(diags), unsafe, unknown)
}
