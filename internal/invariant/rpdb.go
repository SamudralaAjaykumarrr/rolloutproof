package invariant

import (
	"fmt"
	"sort"
	"strings"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/graph"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
)

// RPDB001 is the invariant ID for "destructive column removal while old
// readers remain" (docs/invariants.md).
const RPDB001 = "RP-DB-001"

// RPDB002 is the invariant ID for "destructive column removal while old
// writers remain" (docs/invariants.md).
const RPDB002 = "RP-DB-002"

// Evaluate runs every invariant V1 implements against g, using services
// as the (service, version) -> declared-facts lookup
// (docs/architecture.md §6's EvidenceGap mechanism fires whenever a live
// version has no entry here). It always returns exactly one Diagnostic
// per implemented invariant, in a fixed order, regardless of verdict —
// callers that only care about non-SAFE results filter afterward; the
// full list is what lets a report say "N invariants evaluated, 0
// violated."
func Evaluate(g *graph.Graph, services map[ir.ServiceKey]ir.Service) []ir.Diagnostic {
	return []ir.Diagnostic{
		evaluateDestructiveColumnRemoval(RPDB001, g, services, ir.Service.ReadsColumn, ir.EvidenceSchemaRead, "reads"),
		evaluateDestructiveColumnRemoval(RPDB002, g, services, ir.Service.WritesColumn, ir.EvidenceSchemaWrite, "writes"),
		evaluateNotNullIntroduction(g, services),
		evaluateRenameWithoutCompatibility(g, services),
	}
}

// Aggregate folds a set of Diagnostics into the single overall rollout
// verdict, per the dominance rule Unsafe > Unknown > Safe
// (docs/architecture.md §6).
func Aggregate(diags []ir.Diagnostic) ir.Verdict {
	v := ir.VerdictSafe
	for _, d := range diags {
		v = ir.Combine(v, d.Verdict)
	}
	return v
}

type columnFact func(ir.Service, ir.ColumnRef) bool

// violation is one (state, committed drop op, live version) triple whose
// facts satisfy an invariant's UNSAFE precondition.
type violation struct {
	stateID     int
	op          ir.CommittedOp
	svcVersion  ir.LiveVersion
	svc         ir.Service
	liveInState []ir.LiveVersion
}

// evaluateDestructiveColumnRemoval implements the shared mechanism behind
// RP-DB-001 and RP-DB-002 (docs/invariants.md): both ask "does a live
// version's declared column fact (reads, or writes) overlap a column a
// committed migration in this reachable state has dropped." They differ
// only in which ir.Service accessor and Evidence kind they use — encoded
// here as parameters rather than duplicated logic, so a defect fixed in
// one cannot silently persist in the other.
func evaluateDestructiveColumnRemoval(
	invariantID string,
	g *graph.Graph,
	services map[ir.ServiceKey]ir.Service,
	fact columnFact,
	evidenceKind ir.EvidenceKind,
	factLabel string,
) ir.Diagnostic {
	var violations []violation
	gaps := newGapSet()

	// Iterate nodes in ID order (assigned deterministically at
	// construction, docs/architecture.md §4.3) so that among ties this
	// function's own bookkeeping is deterministic before ShortestPath's
	// own tie-breaking is even consulted.
	nodes := append([]ir.RolloutState(nil), g.Nodes...)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })

	for _, s := range nodes {
		if s.SchemaState.Indeterminate {
			gaps.add(ir.EvidenceGap{
				Field:  fmt.Sprintf("RolloutState %d SchemaState", s.ID),
				Reason: "a migration operation in this reachable state could not be classified (unsupported SQL), so its effect on the schema is unknown",
			})
			continue
		}
		for _, cop := range s.SchemaState.CommittedOps {
			if cop.Op.Kind != ir.OpDropColumn {
				continue
			}
			target := cop.Op.TargetColumn()
			for _, lv := range s.Live {
				key := ir.ServiceKey{Name: lv.ServiceName, Version: lv.Version}
				svc, ok := services[key]
				if !ok {
					gaps.add(ir.EvidenceGap{
						Field:  fmt.Sprintf("Service %s@%s", lv.ServiceName, lv.Version),
						Reason: "no contract metadata file found for this service version",
					})
					continue
				}
				if !svc.TouchesTable(target.Table) {
					gaps.add(ir.EvidenceGap{
						Field:  fmt.Sprintf("Service %s@%s schema access on table %s", lv.ServiceName, lv.Version, target.Table),
						Reason: "contract metadata for this service version does not mention this table",
					})
					continue
				}
				if fact(svc, target) {
					violations = append(violations, violation{stateID: s.ID, op: cop, svcVersion: lv, svc: svc, liveInState: s.Live})
				}
			}
		}
	}

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

	best := selectShortest(g, violations)
	path := g.ShortestPath(best.stateID)
	target := best.op.Op.TargetColumn()

	var summary string
	if len(best.liveInState) > 1 {
		summary = fmt.Sprintf(
			"migration %q drops %s while %s@%s still %s it, and the two may coexist during the rollout",
			best.op.MigrationID, target, best.svcVersion.ServiceName, best.svcVersion.Version, factLabel)
	} else {
		summary = fmt.Sprintf(
			"migration %q drops %s while %s@%s still %s it, and the migration's declared phase does not rule out committing before %s@%s is replaced",
			best.op.MigrationID, target, best.svcVersion.ServiceName, best.svcVersion.Version, factLabel, best.svcVersion.ServiceName, best.svcVersion.Version)
	}

	evidence := []ir.Evidence{
		{
			Kind:        ir.EvidenceMigrationOp,
			Artifact:    best.op.MigrationID,
			Locator:     best.op.Op.Kind.String(),
			Line:        best.op.Op.SourceLine,
			Description: fmt.Sprintf("migration drops %s", target),
		},
		{
			Kind:        evidenceKind,
			Artifact:    best.svc.SourceFile,
			Description: fmt.Sprintf("%s@%s declares it %s %s", best.svcVersion.ServiceName, best.svcVersion.Version, factLabel, target),
		},
		{
			Kind:        ir.EvidenceRolloutStrategy,
			Description: describeReachability(best.liveInState),
		},
	}

	rollbackVerdict := EvaluateRollback(g.State(best.stateID).SchemaState.CommittedOps, best.svc)

	return ir.Diagnostic{
		InvariantID: invariantID,
		Verdict:     ir.VerdictUnsafe,
		Summary:     summary,
		Evidence:    evidence,
		Counterexample: &ir.Counterexample{
			Path:           path,
			ViolatingState: g.State(best.stateID),
			RecommendedSequence: []string{
				fmt.Sprintf("deploy a compatibility release of %s that removes the dependency on %s", best.svcVersion.ServiceName, target),
				"wait for the compatibility release to complete its rollout",
				fmt.Sprintf("verify no live version still declares a dependency on %s", target),
				fmt.Sprintf("apply migration %q", best.op.MigrationID),
			},
		},
		RollbackVerdict: rollbackVerdict,
	}
}

func describeLiveSet(live []ir.LiveVersion) string {
	parts := make([]string, len(live))
	for i, lv := range live {
		parts[i] = fmt.Sprintf("%s@%s", lv.ServiceName, lv.Version)
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// describeReachability names the specific mechanism that makes this
// violating state reachable: coexistence (more than one version live at
// once) if that's what the state shows, or migration-timing ambiguity
// (docs/architecture.md §3.3, assumption A2) when only one version is
// live — the two are distinct hazards and citing the wrong one would
// misdirect a reader toward the wrong remediation (e.g. "reduce
// maxSurge" fixes coexistence but does nothing about timing ambiguity).
func describeReachability(live []ir.LiveVersion) string {
	if len(live) > 1 {
		return fmt.Sprintf("rollout strategy permits the live version set %s to coexist during this rollout", describeLiveSet(live))
	}
	return fmt.Sprintf("the migration's declared phase does not order its commit relative to %s becoming live, so this state is reachable even without version coexistence", describeLiveSet(live))
}

// selectShortest picks the violation whose state is reachable by the
// fewest transition edges from the graph's start (docs/architecture.md
// §4.4); ties are broken by the lowest state ID, which is itself
// deterministic (assigned at construction, docs/architecture.md §4.3).
func selectShortest(g *graph.Graph, violations []violation) violation {
	ids := make([]int, len(violations))
	for i, v := range violations {
		ids[i] = v.stateID
	}
	return violations[shortestByStateID(g, ids)]
}

// shortestByStateID returns the index into ids reachable by the fewest
// transition edges from the graph's start, ties broken by the lowest
// state ID — the counterexample-selection rule every invariant in this
// package shares (docs/architecture.md §4.4), factored out so a defect
// fixed here cannot silently persist in only one invariant's copy.
func shortestByStateID(g *graph.Graph, ids []int) int {
	best := 0
	bestLen := len(g.ShortestPath(ids[0]))
	for i := 1; i < len(ids); i++ {
		l := len(g.ShortestPath(ids[i]))
		if l < bestLen || (l == bestLen && ids[i] < ids[best]) {
			best, bestLen = i, l
		}
	}
	return best
}

// gapSet deduplicates EvidenceGaps (the same missing fact can otherwise
// be reported once per reachable state that needed it) while preserving
// a deterministic, sorted output order.
type gapSet struct {
	seen map[ir.EvidenceGap]struct{}
}

func newGapSet() *gapSet { return &gapSet{seen: map[ir.EvidenceGap]struct{}{}} }

func (g *gapSet) add(gap ir.EvidenceGap) { g.seen[gap] = struct{}{} }

func (g *gapSet) len() int { return len(g.seen) }

func (g *gapSet) list() []ir.EvidenceGap {
	out := make([]ir.EvidenceGap, 0, len(g.seen))
	for gap := range g.seen {
		out = append(out, gap)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Field != out[j].Field {
			return out[i].Field < out[j].Field
		}
		return out[i].Reason < out[j].Reason
	})
	return out
}
