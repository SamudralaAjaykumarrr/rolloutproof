package report

import (
	"encoding/json"
	"testing"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
)

func TestRenderSARIF_ValidAndDeterministic(t *testing.T) {
	diags := unsafeDiags(t)

	a, err := RenderSARIF(diags, "0.1.0-test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b, err := RenderSARIF(diags, "0.1.0-test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(a) != string(b) {
		t.Fatalf("RenderSARIF must be deterministic across repeated calls")
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(a, &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if parsed["version"] != SARIFVersion {
		t.Fatalf("expected version %q, got %v", SARIFVersion, parsed["version"])
	}
	runs, ok := parsed["runs"].([]interface{})
	if !ok || len(runs) != 1 {
		t.Fatalf("expected exactly one run, got %v", parsed["runs"])
	}
}

func TestRenderSARIF_SafeDiagnosticsProduceNoResult(t *testing.T) {
	diags := []ir.Diagnostic{
		{InvariantID: "RP-DB-001", Verdict: ir.VerdictSafe, Summary: "no reachable state violates this invariant"},
	}
	out, err := RenderSARIF(diags, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var log struct {
		Runs []struct {
			Results []interface{} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(out, &log); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(log.Runs[0].Results) != 0 {
		t.Fatalf("expected no SARIF results for an all-SAFE diagnostic set, got %d", len(log.Runs[0].Results))
	}
}

func TestRenderSARIF_UnsafeIsErrorAdvisoryIsWarningUnknownIsNote(t *testing.T) {
	diags := []ir.Diagnostic{
		{InvariantID: "RP-DB-001", Verdict: ir.VerdictUnsafe, Summary: "blocking finding", Counterexample: &ir.Counterexample{Outcome: "x"}},
		{InvariantID: "RP-K8S-004", Verdict: ir.VerdictUnsafe, Advisory: true, Summary: "advisory finding", Counterexample: &ir.Counterexample{Outcome: "y"}},
		{InvariantID: "RP-DB-002", Verdict: ir.VerdictUnknown, Summary: "insufficient evidence", MissingEvidence: []ir.EvidenceGap{{Field: "f", Reason: "r"}}},
	}
	out, err := RenderSARIF(diags, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var log struct {
		Runs []struct {
			Results []struct {
				RuleID string `json:"ruleId"`
				Level  string `json:"level"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(out, &log); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	levels := make(map[string]string, len(log.Runs[0].Results))
	for _, r := range log.Runs[0].Results {
		levels[r.RuleID] = r.Level
	}
	if levels["RP-DB-001"] != "error" {
		t.Fatalf("expected RP-DB-001 level error, got %q", levels["RP-DB-001"])
	}
	if levels["RP-K8S-004"] != "warning" {
		t.Fatalf("expected advisory RP-K8S-004 level warning, got %q", levels["RP-K8S-004"])
	}
	if levels["RP-DB-002"] != "note" {
		t.Fatalf("expected UNKNOWN RP-DB-002 level note, got %q", levels["RP-DB-002"])
	}
}

func TestRenderSARIF_LocationsFromEvidenceArtifacts(t *testing.T) {
	diags := []ir.Diagnostic{
		{
			InvariantID: "RP-DB-001",
			Verdict:     ir.VerdictUnsafe,
			Summary:     "x",
			Evidence: []ir.Evidence{
				{Kind: ir.EvidenceMigrationOp, Artifact: "migrations/017_drop_email.sql", Line: 3, Description: "drops users.email"},
			},
			Counterexample: &ir.Counterexample{Outcome: "x"},
		},
	}
	out, err := RenderSARIF(diags, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var log struct {
		Runs []struct {
			Results []struct {
				Locations []struct {
					PhysicalLocation struct {
						ArtifactLocation struct {
							URI string `json:"uri"`
						} `json:"artifactLocation"`
						Region struct {
							StartLine int `json:"startLine"`
						} `json:"region"`
					} `json:"physicalLocation"`
				} `json:"locations"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(out, &log); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	locs := log.Runs[0].Results[0].Locations
	if len(locs) != 1 {
		t.Fatalf("expected exactly one location, got %d", len(locs))
	}
	if locs[0].PhysicalLocation.ArtifactLocation.URI != "migrations/017_drop_email.sql" {
		t.Fatalf("unexpected URI: %q", locs[0].PhysicalLocation.ArtifactLocation.URI)
	}
	if locs[0].PhysicalLocation.Region.StartLine != 3 {
		t.Fatalf("unexpected start line: %d", locs[0].PhysicalLocation.Region.StartLine)
	}
}

func TestRenderSARIF_RulesListDeduplicatedAndSorted(t *testing.T) {
	diags := []ir.Diagnostic{
		{InvariantID: "RP-DB-002", Verdict: ir.VerdictUnsafe, Summary: "a", Counterexample: &ir.Counterexample{Outcome: "a"}},
		{InvariantID: "RP-DB-001", Verdict: ir.VerdictUnsafe, Summary: "b", Counterexample: &ir.Counterexample{Outcome: "b"}},
		{InvariantID: "RP-DB-001", Verdict: ir.VerdictUnknown, Summary: "c", MissingEvidence: []ir.EvidenceGap{{Field: "f", Reason: "r"}}},
	}
	out, err := RenderSARIF(diags, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var log struct {
		Runs []struct {
			Tool struct {
				Driver struct {
					Rules []struct {
						ID string `json:"id"`
					} `json:"rules"`
				} `json:"driver"`
			} `json:"tool"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(out, &log); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rules := log.Runs[0].Tool.Driver.Rules
	if len(rules) != 2 {
		t.Fatalf("expected 2 deduplicated rules, got %d: %+v", len(rules), rules)
	}
	if rules[0].ID != "RP-DB-001" || rules[1].ID != "RP-DB-002" {
		t.Fatalf("expected sorted rule IDs, got %+v", rules)
	}
}
