package invariant

import (
	"fmt"
	"sort"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/graph"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
)

// RPAPI001 is the invariant ID for "provider removes endpoint while old
// consumer remains" (docs/invariants.md).
const RPAPI001 = "RP-API-001"

// RPAPI002 is the invariant ID for "incompatible request contract"
// (docs/invariants.md).
const RPAPI002 = "RP-API-002"

// RPAPI003 is the invariant ID for "incompatible response contract"
// (docs/invariants.md).
const RPAPI003 = "RP-API-003"

// RPAPI004 is the invariant ID for "mixed-version producer/consumer
// incompatibility" (docs/invariants.md) — the aggregate of RP-API-001/002/003.
const RPAPI004 = "RP-API-004"

// apiViolation is one (state, consumer, provider, operation) tuple whose
// facts satisfy an RP-API precondition. field is empty for a missing
// endpoint (RP-API-001); otherwise names the specific field RP-API-002
// (request) or RP-API-003 (response) found incompatible.
type apiViolation struct {
	stateID      int
	consumerLV   ir.LiveVersion
	consumer     ir.Service
	providerLV   ir.LiveVersion
	contractName string
	operation    string
	field        string
	liveInState  []ir.LiveVersion
}

// anyAPIUsageDeclared reports whether any service anywhere in services
// declares API facts at all — RP-API's "not applicable" precondition,
// the same opt-in pattern RP-DB-006 uses for undeclared expand/contract
// links: a plan that never uses api.provides/api.consumes has nothing
// for this family to evaluate, and must not be penalized for missing
// evidence it was never asked to supply.
func anyAPIUsageDeclared(services map[ir.ServiceKey]ir.Service) bool {
	for _, svc := range services {
		if len(svc.APIProvides()) > 0 || len(svc.APIConsumes()) > 0 {
			return true
		}
	}
	return false
}

// EvaluateAPI implements the RP-API family (docs/invariants.md):
//
//   - RP-API-001: a provider removes an endpoint a live consumer still
//     calls.
//   - RP-API-002: a provider requires a request field a live consumer
//     doesn't send.
//   - RP-API-003: a provider's response drops a field a live consumer
//     requires.
//   - RP-API-004: the aggregate of the three above, evaluated
//     exhaustively across every live provider/consumer pair in each
//     reachable state — docs/invariants.md's "also catches n-way mixed
//     version states... deduplicating diagnostics that would otherwise
//     repeat per pair with the same root cause," which the shared scan
//     below already does by construction (each violation is recorded
//     once per pair, not once per invariant re-derivation).
//
// This is evaluated over every reachable rollout state (docs/architecture.md
// §3-4), not a static contract diff: for each live consumer version's
// declared APIConsumes entry, every live provider version in that same
// state that declares (via contracts, the registry
// internal/parser/contract.Registry.APIContracts builds) it provides the
// named contract is checked — matching docs/invariants.md RP-API-001's
// resolution of its own ambiguous first draft: "the actual risk is the
// consumer calling the endpoint regardless of which provider replica
// answers."
//
// A consumer's dependency on a contract no live version currently
// provides is a gap (UNKNOWN), not silently skipped: RolloutProof cannot
// distinguish "the provider isn't part of this plan" from "the provider
// is part of this plan and its contract metadata is simply incomplete."
func EvaluateAPI(g *graph.Graph, services map[ir.ServiceKey]ir.Service, contracts map[ir.APIContractKey]ir.APIContract) []ir.Diagnostic {
	if !anyAPIUsageDeclared(services) {
		return []ir.Diagnostic{
			notApplicableAPI(RPAPI001), notApplicableAPI(RPAPI002), notApplicableAPI(RPAPI003), notApplicableAPI(RPAPI004),
		}
	}

	var missingEndpoint, missingRequest, missingResponse []apiViolation
	gaps := newGapSet()

	nodes := append([]ir.RolloutState(nil), g.Nodes...)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })

	for _, s := range nodes {
		for _, clv := range s.Live {
			consumer, ok := services[ir.ServiceKey{Name: clv.ServiceName, Version: clv.Version}]
			if !ok {
				gaps.add(ir.EvidenceGap{
					Field:  fmt.Sprintf("Service %s@%s", clv.ServiceName, clv.Version),
					Reason: "no contract metadata file found for this service version",
				})
				continue
			}
			for _, c := range consumer.APIConsumes() {
				matched := false
				for _, plv := range s.Live {
					contract, ok := contracts[ir.APIContractKey{Name: c.ContractName, ProviderService: plv.ServiceName, ProviderVersion: plv.Version}]
					if !ok {
						continue
					}
					matched = true
					ep, ok := contract.Endpoint(c.Operation)
					if !ok {
						missingEndpoint = append(missingEndpoint, apiViolation{
							stateID: s.ID, consumerLV: clv, consumer: consumer, providerLV: plv,
							contractName: c.ContractName, operation: c.Operation, liveInState: s.Live,
						})
						continue
					}
					for _, f := range ep.RequestShape.Fields() {
						if f.Required && !containsString(c.RequestFieldsSent, f.Name) {
							missingRequest = append(missingRequest, apiViolation{
								stateID: s.ID, consumerLV: clv, consumer: consumer, providerLV: plv,
								contractName: c.ContractName, operation: c.Operation, field: f.Name, liveInState: s.Live,
							})
						}
					}
					for _, f := range c.RequiredResponseFields {
						if !ep.ResponseShape.HasField(f) {
							missingResponse = append(missingResponse, apiViolation{
								stateID: s.ID, consumerLV: clv, consumer: consumer, providerLV: plv,
								contractName: c.ContractName, operation: c.Operation, field: f, liveInState: s.Live,
							})
						}
					}
				}
				if !matched {
					gaps.add(ir.EvidenceGap{
						Field:  fmt.Sprintf("APIContract %s (needed by %s@%s)", c.ContractName, clv.ServiceName, clv.Version),
						Reason: "no live service version in this reachable state declares it provides this contract",
					})
				}
			}
		}
	}

	rpapi001 := buildAPIDiagnostic(g, RPAPI001, missingEndpoint, gaps, describeMissingEndpoint, evidenceMissingEndpoint, recommendMissingEndpoint, outcomeMissingEndpoint)
	rpapi002 := buildAPIDiagnostic(g, RPAPI002, missingRequest, gaps, describeMissingRequestField, evidenceMissingField, recommendFieldFix, outcomeMissingRequestField)
	rpapi003 := buildAPIDiagnostic(g, RPAPI003, missingResponse, gaps, describeMissingResponseField, evidenceMissingField, recommendFieldFix, outcomeMissingResponseField)
	rpapi004 := aggregateAPI(rpapi001, rpapi002, rpapi003)

	return []ir.Diagnostic{rpapi001, rpapi002, rpapi003, rpapi004}
}

func notApplicableAPI(id string) ir.Diagnostic {
	return ir.Diagnostic{
		InvariantID: id,
		Verdict:     ir.VerdictSafe,
		Summary:     fmt.Sprintf("%s: not applicable (no service declares api.provides/api.consumes)", id),
	}
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func describeMissingEndpoint(v apiViolation) string {
	return fmt.Sprintf(
		"%s@%s removes operation %q from contract %q, while %s@%s still calls it",
		v.providerLV.ServiceName, v.providerLV.Version, v.operation, v.contractName, v.consumerLV.ServiceName, v.consumerLV.Version)
}

func evidenceMissingEndpoint(v apiViolation) []ir.Evidence {
	return []ir.Evidence{
		{Kind: ir.EvidenceServiceContract, Description: fmt.Sprintf("%s@%s's %q contract no longer declares operation %q", v.providerLV.ServiceName, v.providerLV.Version, v.contractName, v.operation)},
		{Kind: ir.EvidenceServiceContract, Artifact: v.consumer.SourceFile, Description: fmt.Sprintf("%s@%s declares it consumes %q on %q", v.consumerLV.ServiceName, v.consumerLV.Version, v.contractName, v.operation)},
		{Kind: ir.EvidenceRolloutStrategy, Description: describeReachability(v.liveInState)},
	}
}

func outcomeMissingEndpoint(v apiViolation) string {
	return fmt.Sprintf("the consumer's call to %q returns an error — the operation no longer exists", v.operation)
}

func recommendMissingEndpoint(v apiViolation) []string {
	return []string{
		fmt.Sprintf("deploy a compatibility release of %s that stops calling %q", v.consumerLV.ServiceName, v.operation),
		"wait for that release to complete its rollout",
		fmt.Sprintf("verify no live consumer still calls %q on %q", v.operation, v.contractName),
		fmt.Sprintf("remove %q from %s's provided contract", v.operation, v.providerLV.ServiceName),
	}
}

func describeMissingRequestField(v apiViolation) string {
	return fmt.Sprintf(
		"%s@%s requires request field %q on %q, which %s@%s does not send",
		v.providerLV.ServiceName, v.providerLV.Version, v.field, v.operation, v.consumerLV.ServiceName, v.consumerLV.Version)
}

func describeMissingResponseField(v apiViolation) string {
	return fmt.Sprintf(
		"%s@%s's response for %q no longer includes field %q, which %s@%s still requires",
		v.providerLV.ServiceName, v.providerLV.Version, v.operation, v.field, v.consumerLV.ServiceName, v.consumerLV.Version)
}

func evidenceMissingField(v apiViolation) []ir.Evidence {
	return []ir.Evidence{
		{Kind: ir.EvidenceServiceContract, Description: fmt.Sprintf("%s@%s's %q contract on %q: field %q", v.providerLV.ServiceName, v.providerLV.Version, v.contractName, v.operation, v.field)},
		{Kind: ir.EvidenceServiceContract, Artifact: v.consumer.SourceFile, Description: fmt.Sprintf("%s@%s declares a dependency on field %q of %q", v.consumerLV.ServiceName, v.consumerLV.Version, v.field, v.operation)},
		{Kind: ir.EvidenceRolloutStrategy, Description: describeReachability(v.liveInState)},
	}
}

func outcomeMissingRequestField(v apiViolation) string {
	return fmt.Sprintf("the provider rejects the consumer's request to %q for omitting required field %q", v.operation, v.field)
}

func outcomeMissingResponseField(v apiViolation) string {
	return fmt.Sprintf("the consumer cannot find field %q in the response from %q and fails to process it", v.field, v.operation)
}

func recommendFieldFix(v apiViolation) []string {
	return []string{
		fmt.Sprintf("deploy a compatibility release of %s that tolerates the current %q contract", v.consumerLV.ServiceName, v.operation),
		"wait for that release to complete its rollout",
		fmt.Sprintf("verify no live consumer depends on the previous shape of %q", v.operation),
	}
}

// buildAPIDiagnostic is the shared reporting mechanism behind
// RP-API-001/002/003: pick the shortest reachable violation (if any),
// deterministically as every other invariant in this package does
// (docs/architecture.md §4.4), or fall back to gaps -> UNKNOWN, or SAFE.
func buildAPIDiagnostic(
	g *graph.Graph,
	invariantID string,
	violations []apiViolation,
	gaps *gapSet,
	describe func(apiViolation) string,
	evidence func(apiViolation) []ir.Evidence,
	recommend func(apiViolation) []string,
	outcome func(apiViolation) string,
) ir.Diagnostic {
	if len(violations) == 0 {
		if gaps.len() > 0 {
			return ir.Diagnostic{
				InvariantID:     invariantID,
				Verdict:         ir.VerdictUnknown,
				Summary:         fmt.Sprintf("%s: insufficient evidence to evaluate every reachable state", invariantID),
				MissingEvidence: gaps.list(),
			}
		}
		return ir.Diagnostic{
			InvariantID: invariantID,
			Verdict:     ir.VerdictSafe,
			Summary:     fmt.Sprintf("%s: no reachable state violates this invariant", invariantID),
		}
	}

	ids := make([]int, len(violations))
	for i, v := range violations {
		ids[i] = v.stateID
	}
	best := violations[shortestByStateID(g, ids)]
	path := g.ShortestPath(best.stateID)

	return ir.Diagnostic{
		InvariantID: invariantID,
		Verdict:     ir.VerdictUnsafe,
		Summary:     describe(best),
		Evidence:    evidence(best),
		Counterexample: &ir.Counterexample{
			Path:                path,
			ViolatingState:      g.State(best.stateID),
			Outcome:             outcome(best),
			RecommendedSequence: recommend(best),
		},
	}
}

// aggregateAPI implements RP-API-004 as the named combination of
// RP-API-001/002/003's results (docs/invariants.md: "not a new checking
// algorithm... this one evaluates exhaustively across all live pairs in
// a given state, deduplicating diagnostics").
func aggregateAPI(rpapi001, rpapi002, rpapi003 ir.Diagnostic) ir.Diagnostic {
	verdict := ir.Combine(ir.Combine(rpapi001.Verdict, rpapi002.Verdict), rpapi003.Verdict)
	switch verdict {
	case ir.VerdictUnsafe:
		var counterexample *ir.Counterexample
		for _, d := range []ir.Diagnostic{rpapi001, rpapi002, rpapi003} {
			if d.Verdict == ir.VerdictUnsafe {
				counterexample = d.Counterexample
				break
			}
		}
		return ir.Diagnostic{
			InvariantID:    RPAPI004,
			Verdict:        ir.VerdictUnsafe,
			Summary:        fmt.Sprintf("%s: at least one live provider/consumer pair is incompatible (RP-API-001: %s, RP-API-002: %s, RP-API-003: %s)", RPAPI004, rpapi001.Verdict, rpapi002.Verdict, rpapi003.Verdict),
			Counterexample: counterexample,
		}
	case ir.VerdictUnknown:
		// RP-API-001/002/003 independently discover the same missing
		// evidence whenever the same consumer/contract pair is the
		// reason all three can't be evaluated (they share one scan —
		// see EvaluateAPI) — deduplicated here via gapSet the same way
		// every other invariant's own gap collection already is, so the
		// aggregate doesn't repeat one gap three times.
		gaps := newGapSet()
		for _, d := range []ir.Diagnostic{rpapi001, rpapi002, rpapi003} {
			for _, g := range d.MissingEvidence {
				gaps.add(g)
			}
		}
		return ir.Diagnostic{
			InvariantID:     RPAPI004,
			Verdict:         ir.VerdictUnknown,
			Summary:         fmt.Sprintf("%s: insufficient evidence to confirm every live provider/consumer pair is compatible", RPAPI004),
			MissingEvidence: gaps.list(),
		}
	default:
		return ir.Diagnostic{
			InvariantID: RPAPI004,
			Verdict:     ir.VerdictSafe,
			Summary:     fmt.Sprintf("%s: every live provider/consumer pair is compatible", RPAPI004),
		}
	}
}
