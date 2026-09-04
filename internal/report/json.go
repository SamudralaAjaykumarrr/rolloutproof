package report

import (
	"encoding/json"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/invariant"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
)

// JSONSchemaVersion identifies the shape of RenderJSON's output, so a
// consumer can detect a breaking change before parsing fails silently
// (docs/adr/0006's "adding a format never touches invariant logic"
// extends to "changing the format is itself a visible, versioned
// event," not a silent drift).
const JSONSchemaVersion = "rolloutproof.dev/report/v1"

// jsonReport is the top-level machine-readable report. Field order here
// is JSON key order (encoding/json marshals struct fields in
// declaration order, never map iteration order), and Invariants
// preserves the exact order Diagnostics were evaluated in — the same
// determinism guarantee RenderText already has (docs/vision.md §9).
type jsonReport struct {
	SchemaVersion string          `json:"schemaVersion"`
	Verdict       string          `json:"verdict"`
	Summary       string          `json:"summary"`
	Invariants    []jsonInvariant `json:"invariants"`
}

type jsonInvariant struct {
	ID      string `json:"id"`
	Verdict string `json:"verdict"`
	// Advisory marks a coarse, over-inclusive finding that does not by
	// itself veto the overall Verdict above (ir.Diagnostic.Advisory).
	Advisory        bool                `json:"advisory"`
	Summary         string              `json:"summary"`
	Evidence        []jsonEvidence      `json:"evidence,omitempty"`
	Counterexample  *jsonCounterexample `json:"counterexample,omitempty"`
	MissingEvidence []jsonGap           `json:"missingEvidence,omitempty"`
	// RollbackVerdict is the four-state SAFE/CONDITIONALLY_SAFE/UNSAFE/UNKNOWN
	// classification (docs/architecture.md §5); "UNKNOWN" here means
	// "not evaluated as part of this diagnostic," per
	// ir.Diagnostic.RollbackVerdict's own zero-value-safety convention,
	// not necessarily genuine uncertainty.
	RollbackVerdict string `json:"rollbackVerdict"`
}

type jsonEvidence struct {
	Kind        string `json:"kind"`
	Artifact    string `json:"artifact,omitempty"`
	Locator     string `json:"locator,omitempty"`
	Line        int    `json:"line,omitempty"`
	Description string `json:"description"`
}

type jsonCounterexample struct {
	Path                []jsonEvent `json:"path"`
	Outcome             string      `json:"outcome,omitempty"`
	RecommendedSequence []string    `json:"recommendedSequence,omitempty"`
}

type jsonEvent struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

type jsonGap struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

// RenderJSON renders the overall rollout verdict and every diagnostic as
// stable, deterministic, indented JSON — every field traces back to an
// ir.Diagnostic field, never a hand-written string (same discipline as
// RenderText, docs/adr/0006).
func RenderJSON(diags []ir.Diagnostic) ([]byte, error) {
	rep := jsonReport{
		SchemaVersion: JSONSchemaVersion,
		Verdict:       invariant.Aggregate(diags).String(),
		Summary:       Summary(diags),
		Invariants:    make([]jsonInvariant, 0, len(diags)),
	}
	for _, d := range diags {
		rep.Invariants = append(rep.Invariants, toJSONInvariant(d))
	}
	return json.MarshalIndent(rep, "", "  ")
}

func toJSONInvariant(d ir.Diagnostic) jsonInvariant {
	ji := jsonInvariant{
		ID:              d.InvariantID,
		Verdict:         d.Verdict.String(),
		Advisory:        d.Advisory,
		Summary:         d.Summary,
		RollbackVerdict: d.RollbackVerdict.String(),
	}
	for _, e := range d.Evidence {
		ji.Evidence = append(ji.Evidence, jsonEvidence{
			Kind:        e.Kind.String(),
			Artifact:    e.Artifact,
			Locator:     e.Locator,
			Line:        e.Line,
			Description: e.Description,
		})
	}
	if d.Counterexample != nil {
		jc := &jsonCounterexample{
			Outcome:             d.Counterexample.Outcome,
			RecommendedSequence: d.Counterexample.RecommendedSequence,
		}
		for _, ev := range d.Counterexample.Path {
			jc.Path = append(jc.Path, jsonEvent{Kind: ev.Kind.String(), Detail: ev.Detail})
		}
		ji.Counterexample = jc
	}
	for _, g := range d.MissingEvidence {
		ji.MissingEvidence = append(ji.MissingEvidence, jsonGap{Field: g.Field, Reason: g.Reason})
	}
	return ji
}
