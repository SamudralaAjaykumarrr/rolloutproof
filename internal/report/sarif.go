// SARIF rendering (docs/vision.md §7's planned JSON/SARIF formats).
// SARIF (Static Analysis Results Interchange Format, OASIS) is what
// GitHub code scanning and most CI static-analysis integrations consume,
// so this is what makes `rolloutproof verify --format sarif` usable
// directly as a GitHub Actions code-scanning upload.
package report

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
)

// SARIFVersion is the SARIF spec version this output conforms to.
const SARIFVersion = "2.1.0"

// SARIFSchemaURI is the canonical schema $schema value tools expect.
const SARIFSchemaURI = "https://raw.githubusercontent.com/oasis-tcs/sarif-spec/master/Schemata/sarif-schema-2.1.0.json"

// ToolName and ToolInformationURI identify RolloutProof in a SARIF
// consumer's UI (e.g. GitHub's code-scanning alert list groups by tool).
const (
	ToolName           = "rolloutproof"
	ToolInformationURI = "https://github.com/SamudralaAjaykumarrr/rolloutproof"
)

type sarifLog struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool    sarifTool     `json:"tool"`
	Results []sarifResult `json:"results"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name           string      `json:"name"`
	InformationURI string      `json:"informationUri"`
	Version        string      `json:"version"`
	Rules          []sarifRule `json:"rules"`
}

type sarifRule struct {
	ID               string          `json:"id"`
	ShortDescription sarifText       `json:"shortDescription"`
	FullDescription  sarifText       `json:"fullDescription,omitempty"`
	Properties       *sarifRuleProps `json:"properties,omitempty"`
}

type sarifRuleProps struct {
	Tags []string `json:"tags,omitempty"`
}

type sarifText struct {
	Text string `json:"text"`
}

type sarifResult struct {
	RuleID     string            `json:"ruleId"`
	Level      string            `json:"level"`
	Message    sarifText         `json:"message"`
	Locations  []sarifLocation   `json:"locations,omitempty"`
	Kind       string            `json:"kind,omitempty"`
	Properties *sarifResultProps `json:"properties,omitempty"`
}

type sarifResultProps struct {
	Advisory        bool     `json:"advisory"`
	RollbackVerdict string   `json:"rollbackVerdict,omitempty"`
	Recommendation  []string `json:"recommendation,omitempty"`
}

type sarifLocation struct {
	PhysicalLocation sarifPhysicalLocation `json:"physicalLocation"`
}

type sarifPhysicalLocation struct {
	ArtifactLocation sarifArtifactLocation `json:"artifactLocation"`
	Region           *sarifRegion          `json:"region,omitempty"`
}

type sarifArtifactLocation struct {
	URI string `json:"uri"`
}

type sarifRegion struct {
	StartLine int `json:"startLine"`
}

// RenderSARIF renders diags as a SARIF 2.1.0 log with one run. toolVersion
// is embedded in the driver so a consumer can tell which RolloutProof
// build produced a given set of results (cmd/rolloutproof's own
// --version output, see internal/version).
//
// SAFE diagnostics produce no result (there is nothing to report); every
// UNSAFE diagnostic becomes an "error" (or "warning" if
// ir.Diagnostic.Advisory — a coarse, non-blocking finding, never
// conflated with a high-confidence one); every UNKNOWN diagnostic
// becomes a "note", since missing evidence is not itself a proven
// defect (docs/vision.md §10) but is still worth surfacing in a
// code-scanning UI. The rules list always includes every invariant ID
// that produced a result, deduplicated and sorted, so a SARIF consumer
// can resolve ruleId without a separate lookup.
func RenderSARIF(diags []ir.Diagnostic, toolVersion string) ([]byte, error) {
	run := sarifRun{
		Tool: sarifTool{Driver: sarifDriver{
			Name:           ToolName,
			InformationURI: ToolInformationURI,
			Version:        toolVersion,
		}},
	}

	ruleIDs := make(map[string]bool)
	for _, d := range diags {
		if d.Verdict == ir.VerdictSafe {
			continue
		}
		ruleIDs[d.InvariantID] = true
		run.Results = append(run.Results, toSARIFResult(d))
	}

	ids := make([]string, 0, len(ruleIDs))
	for id := range ruleIDs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		run.Tool.Driver.Rules = append(run.Tool.Driver.Rules, sarifRule{
			ID:               id,
			ShortDescription: sarifText{Text: fmt.Sprintf("RolloutProof invariant %s (see docs/invariants.md)", id)},
		})
	}

	log := sarifLog{Schema: SARIFSchemaURI, Version: SARIFVersion, Runs: []sarifRun{run}}
	return json.MarshalIndent(log, "", "  ")
}

func toSARIFResult(d ir.Diagnostic) sarifResult {
	level := "note"
	kind := "informational"
	switch d.Verdict {
	case ir.VerdictUnsafe:
		kind = "fail"
		if d.Advisory {
			level = "warning"
		} else {
			level = "error"
		}
	case ir.VerdictUnknown:
		level = "note"
		kind = "review"
	}

	r := sarifResult{
		RuleID:  d.InvariantID,
		Level:   level,
		Kind:    kind,
		Message: sarifText{Text: sarifMessage(d)},
		Properties: &sarifResultProps{
			Advisory:        d.Advisory,
			RollbackVerdict: d.RollbackVerdict.String(),
		},
	}
	if d.Counterexample != nil {
		r.Properties.Recommendation = d.Counterexample.RecommendedSequence
	}

	seenURI := make(map[string]bool)
	for _, e := range d.Evidence {
		if e.Artifact == "" || seenURI[e.Artifact] {
			continue
		}
		seenURI[e.Artifact] = true
		loc := sarifLocation{PhysicalLocation: sarifPhysicalLocation{
			ArtifactLocation: sarifArtifactLocation{URI: e.Artifact},
		}}
		if e.Line > 0 {
			loc.PhysicalLocation.Region = &sarifRegion{StartLine: e.Line}
		}
		r.Locations = append(r.Locations, loc)
	}
	return r
}

func sarifMessage(d ir.Diagnostic) string {
	if d.Verdict == ir.VerdictUnknown {
		return fmt.Sprintf("%s: %s", d.InvariantID, d.Summary)
	}
	msg := d.Summary
	if d.Counterexample != nil && d.Counterexample.Outcome != "" {
		msg = fmt.Sprintf("%s (%s)", msg, d.Counterexample.Outcome)
	}
	return msg
}
