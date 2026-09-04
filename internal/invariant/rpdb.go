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

// Evaluate runs every column-compatibility invariant V1 implements
// (the RP-DB family below RP-DB-006/007) against g, using services as
// the (service, version) -> declared-facts lookup (docs/architecture.md
// §6's EvidenceGap mechanism fires whenever a live version has no entry
// here). plan is needed only by RP-DB-006, which reasons about declared
// expand/contract relationships rather than reachable states — see
// EvaluateRollback (rollback_plan.go) for RP-DB-007 and the RP-ROLLBACK
// family, which additionally need plan.RollbackTarget and are evaluated
// separately since they build their own transition graph.
//
// Evaluate always returns exactly one Diagnostic per implemented
// invariant, in a fixed order, regardless of verdict — callers that only
// care about non-SAFE results filter afterward; the full list is what
// lets a report say "N invariants evaluated, 0 violated."
func Evaluate(plan ir.RolloutPlan, g *graph.Graph, services map[ir.ServiceKey]ir.Service) []ir.Diagnostic {
	return evaluateColumnFamily(plan, g, services, true)
}

// evaluateColumnFamily is Evaluate's implementation, parameterized by
// vacuousWhenNoOp so rollback_plan.go's evaluateOldVersionAgainstCurrentSchema
// can re-run the same RP-DB checks against a rollback's own graph without
// docs/scenario-corpus.md SC-SAFE-004's "no migration in this plan is
// vacuously SAFE" rule suppressing them — a rollback plan legitimately
// commits no migrations of its own (RP-ROLLBACK-002's operational-only
// scope, docs/architecture.md §5) while still needing every check to run
// against whatever schema the forward plan already left behind. See
// evaluateColumnExistence and evaluateNotNullIntroduction's own doc
// comments for why each needs this distinction.
func evaluateColumnFamily(plan ir.RolloutPlan, g *graph.Graph, services map[ir.ServiceKey]ir.Service, vacuousWhenNoOp bool) []ir.Diagnostic {
	return []ir.Diagnostic{
		evaluateColumnExistence(RPDB001, g, services, ir.Service.SchemaReads, ir.EvidenceSchemaRead, "reads", vacuousWhenNoOp),
		evaluateColumnExistence(RPDB002, g, services, ir.Service.SchemaWrites, ir.EvidenceSchemaWrite, "writes", vacuousWhenNoOp),
		evaluateTypeCompatibility(g, services),
		evaluateNotNullIntroduction(g, services, vacuousWhenNoOp),
		evaluateRenameWithoutCompatibility(g, services),
		evaluateExpandContractSequence(plan, g),
	}
}

// Aggregate folds a set of Diagnostics into the single overall rollout
// verdict, per the dominance rule Unsafe > Unknown > Safe
// (docs/architecture.md §6).
// Aggregate skips an Advisory diagnostic's own contribution when it is
// UNSAFE — a coarse, over-inclusive finding (docs/invariants.md
// RP-K8S-004) is still reported in full by report.RenderText, but does
// not by itself veto the overall rollout verdict, keeping it visibly
// distinguished from high-confidence findings (ir.Diagnostic.Advisory's
// own doc comment). An Advisory diagnostic reporting UNKNOWN (none do
// today, but the rule is general) still dominates normally — the
// exemption is specifically for the coarse-recall UNSAFE case docs
// describe, not for uncertainty.
func Aggregate(diags []ir.Diagnostic) ir.Verdict {
	v := ir.VerdictSafe
	for _, d := range diags {
		if d.Advisory && d.Verdict == ir.VerdictUnsafe {
			continue
		}
		v = ir.Combine(v, d.Verdict)
	}
	return v
}

// violation is one (state, affected column, live version) triple whose
// facts satisfy an invariant's UNSAFE precondition.
//
// RP-DB-003/004 (rpdb003.go, rpdb004.go) always have a single concrete
// causing op and populate op directly. RP-DB-001/002
// (evaluateColumnExistence, below) instead populate col directly and
// removingOp only when a specific op is responsible — nil means col was
// never present in the plan's schema timeline at all (the
// SC-UNSAFE-004/005 case: a version depending on a column no migration in
// this plan has added yet, so there is no "op" to cite).
type violation struct {
	stateID     int
	op          ir.CommittedOp
	col         ir.ColumnRef
	removingOp  *ir.CommittedOp
	svcVersion  ir.LiveVersion
	svc         ir.Service
	liveInState []ir.LiveVersion
}

// evaluateColumnExistence implements the shared mechanism behind RP-DB-001
// and RP-DB-002 (docs/invariants.md): a live version's declared
// SchemaReads (or SchemaWrites) entry must exist in the schema actually
// committed at every reachable state where that version is live — not
// merely "must not be later dropped." This is the general existence
// check docs/scenario-corpus.md SC-UNSAFE-004/005 describe as "RP-DB-001's
// underlying existence-check mechanism, applied symmetrically" to a
// column that was never added, not only one that was removed; a dropped
// column is simply the special case where HasColumn was true earlier and
// becomes false partway through the timeline.
//
// A column absent because of an OpRenameColumn is deliberately excluded
// here and left to RP-DB-005 alone (docs/invariants.md RP-DB-005: "a
// rename-caused failure should read as 'RP-DB-005: rename' not
// 'RP-DB-001: drop'... sharing one tested code path" — the code path
// shared is Reversibility/counterexample-selection machinery, not this
// function itself reporting the same finding twice under two IDs).
//
// If vacuousWhenNoOp is true and the plan commits no migration operations
// anywhere in the graph, this invariant reports SAFE outright without
// consulting services at all — docs/scenario-corpus.md SC-SAFE-004: "No
// Migration present at all — every RP-DB precondition is vacuously
// unsatisfied." A rollout that changes no schema is out of RP-DB-001/002's
// scope regardless of whether contract metadata happens to be available,
// the same "not applicable" treatment RP-DB-006 gives an undeclared
// expand/contract relationship. vacuousWhenNoOp is false only when
// re-evaluating a rollback's own graph (evaluateColumnFamily), whose
// BaseSchema already reflects whatever the forward plan committed even
// though the rollback graph itself commits nothing further — see
// evaluateColumnFamily's doc comment.
func evaluateColumnExistence(
	invariantID string,
	g *graph.Graph,
	services map[ir.ServiceKey]ir.Service,
	columnsOf func(ir.Service) []ir.ColumnRef,
	evidenceKind ir.EvidenceKind,
	factLabel string,
	vacuousWhenNoOp bool,
) ir.Diagnostic {
	if vacuousWhenNoOp && !graphHasAnyCommittedOp(g) {
		return ir.Diagnostic{
			InvariantID: invariantID,
			Verdict:     ir.VerdictSafe,
			Summary:     fmt.Sprintf("%s: not applicable (this plan commits no migration)", invariantID),
		}
	}

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
			for _, col := range columnsOf(svc) {
				if s.SchemaState.Schema.HasColumn(col) {
					continue
				}
				removingOp, found := findRemovingOp(s.SchemaState.CommittedOps, col)
				if found && removingOp.Op.Kind == ir.OpRenameColumn {
					continue // RP-DB-005's exclusive territory
				}
				var opPtr *ir.CommittedOp
				if found {
					opPtr = &removingOp
				}
				violations = append(violations, violation{stateID: s.ID, col: col, removingOp: opPtr, svcVersion: lv, svc: svc, liveInState: s.Live})
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
	target := best.col

	var causeText, migID string
	if best.removingOp != nil {
		causeText = fmt.Sprintf("migration %q drops %s", best.removingOp.MigrationID, target)
		migID = best.removingOp.MigrationID
	} else {
		causeText = fmt.Sprintf("%s has not been added by any migration in this plan", target)
	}

	var summary string
	if len(best.liveInState) > 1 {
		summary = fmt.Sprintf(
			"%s@%s still %s %s, which does not exist in the schema committed in this reachable state (%s), and the two may coexist during the rollout",
			best.svcVersion.ServiceName, best.svcVersion.Version, factLabel, target, causeText)
	} else {
		summary = fmt.Sprintf(
			"%s@%s still %s %s, which does not exist in the schema committed in this reachable state (%s)",
			best.svcVersion.ServiceName, best.svcVersion.Version, factLabel, target, causeText)
	}

	evidence := []ir.Evidence{
		{
			Kind:        ir.EvidenceMigrationOp,
			Artifact:    migID,
			Description: causeText,
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

	var recommended []string
	if best.removingOp != nil {
		recommended = []string{
			fmt.Sprintf("deploy a compatibility release of %s that removes the dependency on %s", best.svcVersion.ServiceName, target),
			"wait for the compatibility release to complete its rollout",
			fmt.Sprintf("verify no live version still declares a dependency on %s", target),
			fmt.Sprintf("apply migration %q", migID),
		}
	} else {
		recommended = []string{
			fmt.Sprintf("add the migration that creates %s before %s@%s becomes live", target, best.svcVersion.ServiceName, best.svcVersion.Version),
			"or delay this version's rollout until that migration has committed",
		}
	}

	rollbackVerdict := EvaluateRollback(g.State(best.stateID).SchemaState.CommittedOps, best.svc)

	return ir.Diagnostic{
		InvariantID: invariantID,
		Verdict:     ir.VerdictUnsafe,
		Summary:     summary,
		Evidence:    evidence,
		Counterexample: &ir.Counterexample{
			Path:                path,
			ViolatingState:      g.State(best.stateID),
			Outcome:             "the live version's declared schema access no longer matches the committed schema",
			RecommendedSequence: recommended,
		},
		RollbackVerdict: rollbackVerdict,
	}
}

// graphHasAnyCommittedOp reports whether any reachable state in g reflects
// at least one committed migration operation — i.e., whether this plan's
// schema ever differs from its BaseSchema anywhere in the graph.
func graphHasAnyCommittedOp(g *graph.Graph) bool {
	for _, s := range g.Nodes {
		if len(s.SchemaState.CommittedOps) > 0 {
			return true
		}
	}
	return false
}

// findRemovingOp finds the CommittedOp responsible for col not existing
// in this state's schema — an OpDropColumn or OpRenameColumn naming col
// as its (old) column — scanning in reverse so the most recently
// committed matching op wins. found=false means col was simply never
// present in the plan's schema timeline (docs/scenario-corpus.md
// SC-UNSAFE-004/005), not removed from it.
func findRemovingOp(committed []ir.CommittedOp, col ir.ColumnRef) (op ir.CommittedOp, found bool) {
	for i := len(committed) - 1; i >= 0; i-- {
		cop := committed[i]
		if cop.Op.Table != col.Table {
			continue
		}
		switch cop.Op.Kind {
		case ir.OpDropColumn, ir.OpRenameColumn:
			if cop.Op.Column == col.Column {
				return cop, true
			}
		}
	}
	return ir.CommittedOp{}, false
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
