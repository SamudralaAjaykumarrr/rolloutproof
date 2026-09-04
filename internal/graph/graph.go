package graph

import (
	"fmt"
	"sort"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
)

// Edge is one transition graph edge: a mechanical event moving the
// rollout from one RolloutState to another (docs/architecture.md §4.1).
type Edge struct {
	From, To int
	Event    ir.TransitionEvent
}

// Graph is the built transition graph for one RolloutPlan
// (docs/architecture.md §4). Nodes are indexed by their RolloutState.ID.
type Graph struct {
	Nodes  []ir.RolloutState
	Edges  []Edge
	Start  int
	Target int

	adjacency [][]Edge // adjacency[nodeID] = outgoing edges, sorted deterministically
}

// State returns the RolloutState for a node ID.
func (g *Graph) State(id int) ir.RolloutState { return g.Nodes[id] }

// ShortestPath returns the ordered transition events along the
// shortest path from g.Start to target, using a deterministic BFS that
// always visits outgoing edges in a fixed order (by EventKind, then
// lexically by Detail — docs/architecture.md §4.3) so that repeated
// calls, and repeated builds from identical input, produce identical
// results (docs/vision.md §9). Returns nil if target is unreachable
// (which should not occur for any node this package itself produced).
type bfsVisit struct {
	via   Edge
	found bool
}

func (g *Graph) ShortestPath(target int) []ir.TransitionEvent {
	if target == g.Start {
		return nil
	}
	visited := make([]bfsVisit, len(g.Nodes))
	visited[g.Start].found = true

	queue := []int{g.Start}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, e := range g.adjacency[cur] {
			if visited[e.To].found {
				continue
			}
			visited[e.To] = bfsVisit{via: e, found: true}
			if e.To == target {
				return reconstructPath(visited, target, g.Start)
			}
			queue = append(queue, e.To)
		}
	}
	return nil
}

func reconstructPath(visited []bfsVisit, target, start int) []ir.TransitionEvent {
	var events []ir.TransitionEvent
	cur := target
	for cur != start {
		v := visited[cur]
		events = append([]ir.TransitionEvent{v.via.Event}, events...)
		cur = v.via.From
	}
	return events
}

func sortEdges(edges []Edge) {
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].To != edges[j].To {
			return edges[i].To < edges[j].To
		}
		if edges[i].Event.Kind != edges[j].Event.Kind {
			return edges[i].Event.Kind < edges[j].Event.Kind
		}
		return edges[i].Event.Detail < edges[j].Event.Detail
	})
}

// --- construction ---

// workloadStep is one point in a single workload's linear progression.
type workloadStep struct {
	live          []ir.LiveVersion
	eventFromPrev ir.TransitionEvent // zero value for step 0
}

// progression returns the ordered steps one WorkloadChange passes
// through, per docs/architecture.md §3.2 step 2.
func progression(wc ir.WorkloadChange) []workloadStep {
	svc := wc.Workload.ServiceName
	old := ir.LiveVersion{ServiceName: svc, Version: wc.FromVersion}
	neu := ir.LiveVersion{ServiceName: svc, Version: wc.ToVersion}

	if wc.Workload.Strategy.AllowsCoexistence(wc.Workload.Replicas) {
		return []workloadStep{
			{live: []ir.LiveVersion{old}},
			{
				live: []ir.LiveVersion{old, neu},
				eventFromPrev: ir.TransitionEvent{
					Kind:   ir.EventNewReplicaReady,
					Detail: fmt.Sprintf("a %s@%s replica becomes ready while %s@%s remains live", svc, wc.ToVersion, svc, wc.FromVersion),
				},
			},
			{
				live: []ir.LiveVersion{neu},
				eventFromPrev: ir.TransitionEvent{
					Kind:   ir.EventOldReplicaTerminated,
					Detail: fmt.Sprintf("the last %s@%s replica terminates", svc, wc.FromVersion),
				},
			},
		}
	}

	// Recreate, or a RollingUpdate configured with no possible
	// coexistence window: old fully drains before new starts
	// (docs/architecture.md §3.2 step 2).
	return []workloadStep{
		{live: []ir.LiveVersion{old}},
		{
			live: nil,
			eventFromPrev: ir.TransitionEvent{
				Kind:   ir.EventOldReplicaTerminated,
				Detail: fmt.Sprintf("%s@%s fully terminates before %s@%s starts", svc, wc.FromVersion, svc, wc.ToVersion),
			},
		},
		{
			live: []ir.LiveVersion{neu},
			eventFromPrev: ir.TransitionEvent{
				Kind:   ir.EventNewReplicaReady,
				Detail: fmt.Sprintf("%s@%s becomes ready", svc, wc.ToVersion),
			},
		},
	}
}

// versionCombo is one point in the cartesian product of every workload's
// progression: one step index per workload.
type versionCombo struct {
	steps []int
}

func buildVersionCombos(progressions [][]workloadStep) []versionCombo {
	combos := []versionCombo{{steps: make([]int, len(progressions))}}
	for w, steps := range progressions {
		var next []versionCombo
		for _, c := range combos {
			for stepIdx := range steps {
				nc := versionCombo{steps: append([]int(nil), c.steps...)}
				nc.steps[w] = stepIdx
				next = append(next, nc)
			}
		}
		combos = next
	}
	return combos
}

func comboIndex(progressions [][]workloadStep, c versionCombo) int {
	idx := 0
	for w := range progressions {
		idx = idx*len(progressions[w]) + c.steps[w]
	}
	return idx
}

func liveFor(progressions [][]workloadStep, c versionCombo) []ir.LiveVersion {
	var live []ir.LiveVersion
	for w, stepIdx := range c.steps {
		live = append(live, progressions[w][stepIdx].live...)
	}
	return live
}

// Build constructs the transition graph for plan (docs/architecture.md
// §3-4). It never returns an error for a well-formed ir.RolloutPlan
// (ir.NewRolloutPlan already rejects the malformed cases at construction
// time) — the error return exists for the rare case where a migration's
// operations cannot be applied to a schema at all (e.g. an operation
// referencing a table Build cannot resolve because an earlier
// OpUnclassified operation left the schema state genuinely unknown from
// that point forward; see ir.SchemaState.Indeterminate).
func Build(plan ir.RolloutPlan) (*Graph, error) {
	var beforeMigs, duringMigs, afterMigs []ir.MigrationTiming
	for _, mt := range plan.Migrations {
		switch mt.Phase {
		case ir.PhaseBeforeRollout:
			beforeMigs = append(beforeMigs, mt)
		case ir.PhaseDuringRollout:
			duringMigs = append(duringMigs, mt)
		case ir.PhaseAfterRollout:
			afterMigs = append(afterMigs, mt)
		default:
			return nil, fmt.Errorf("graph: migration %q has no valid phase", mt.Migration.ID)
		}
	}

	baseSchema, baseCommitted, baseIndeterminate := applyAll(plan.BaseSchema, nil, beforeMigs)

	progressions := make([][]workloadStep, len(plan.Workloads))
	for i, wc := range plan.Workloads {
		progressions[i] = progression(wc)
	}
	combos := buildVersionCombos(progressions)

	numDuring := len(duringMigs)
	numDuringVectors := 1 << numDuring

	g := &Graph{}
	// nodeID(comboIdx, duringVector) — comboIdx and duringVector are both
	// already deterministic (constructed by nested loops in fixed
	// order), so simple row-major assignment preserves determinism.
	nodeID := func(comboIdx, duringVector int) int {
		return comboIdx*numDuringVectors + duringVector
	}

	for _, c := range combos {
		comboIdx := comboIndex(progressions, c)
		live := liveFor(progressions, c)
		for dv := 0; dv < numDuringVectors; dv++ {
			schema, committed, indeterminate := applyDuringSubset(baseSchema, baseCommitted, baseIndeterminate, duringMigs, dv)
			state := ir.NewRolloutState(nodeID(comboIdx, dv), live, ir.SchemaState{
				Schema:        schema,
				CommittedOps:  committed,
				Indeterminate: indeterminate,
			})
			g.Nodes = append(g.Nodes, state)
		}
	}

	// Version-lattice edges: hold duringVector fixed, advance exactly one
	// workload by one step.
	for _, c := range combos {
		comboIdx := comboIndex(progressions, c)
		for w := range progressions {
			if c.steps[w]+1 >= len(progressions[w]) {
				continue
			}
			nc := versionCombo{steps: append([]int(nil), c.steps...)}
			nc.steps[w]++
			nextComboIdx := comboIndex(progressions, nc)
			event := progressions[w][nc.steps[w]].eventFromPrev
			for dv := 0; dv < numDuringVectors; dv++ {
				g.Edges = append(g.Edges, Edge{From: nodeID(comboIdx, dv), To: nodeID(nextComboIdx, dv), Event: event})
			}
		}
	}

	// During-migration hypercube edges: hold version combo fixed, flip
	// exactly one pending migration to committed.
	for _, c := range combos {
		comboIdx := comboIndex(progressions, c)
		for dv := 0; dv < numDuringVectors; dv++ {
			for k := 0; k < numDuring; k++ {
				if dv&(1<<k) != 0 {
					continue
				}
				next := dv | (1 << k)
				event := ir.TransitionEvent{
					Kind:   ir.EventMigrationOpCommitted,
					Detail: fmt.Sprintf("migration %q commits", duringMigs[k].Migration.ID),
				}
				g.Edges = append(g.Edges, Edge{From: nodeID(comboIdx, dv), To: nodeID(comboIdx, next), Event: event})
			}
		}
	}

	startCombo := versionCombo{steps: make([]int, len(progressions))}
	g.Start = nodeID(comboIndex(progressions, startCombo), 0)

	finalSteps := make([]int, len(progressions))
	for w := range progressions {
		finalSteps[w] = len(progressions[w]) - 1
	}
	finalCombo := versionCombo{steps: finalSteps}
	rolloutComplete := nodeID(comboIndex(progressions, finalCombo), numDuringVectors-1)

	// Append the unambiguous PhaseAfterRollout chain.
	target := rolloutComplete
	schema := g.Nodes[rolloutComplete].SchemaState.Schema
	committed := append([]ir.CommittedOp(nil), g.Nodes[rolloutComplete].SchemaState.CommittedOps...)
	indeterminate := g.Nodes[rolloutComplete].SchemaState.Indeterminate
	live := g.Nodes[rolloutComplete].Live

	for _, mt := range afterMigs {
		nextSchema, nextCommitted, nextIndeterminate := applyOne(schema, committed, indeterminate, mt)
		newID := len(g.Nodes)
		state := ir.NewRolloutState(newID, cloneLive(live), ir.SchemaState{
			Schema:        nextSchema,
			CommittedOps:  nextCommitted,
			Indeterminate: nextIndeterminate,
		})
		g.Nodes = append(g.Nodes, state)
		g.Edges = append(g.Edges, Edge{
			From: target,
			To:   newID,
			Event: ir.TransitionEvent{
				Kind:   ir.EventMigrationOpCommitted,
				Detail: fmt.Sprintf("migration %q commits", mt.Migration.ID),
			},
		})
		target = newID
		schema, committed, indeterminate = nextSchema, nextCommitted, nextIndeterminate
	}
	g.Target = target

	g.adjacency = make([][]Edge, len(g.Nodes))
	for _, e := range g.Edges {
		g.adjacency[e.From] = append(g.adjacency[e.From], e)
	}
	for i := range g.adjacency {
		sortEdges(g.adjacency[i])
	}

	return g, nil
}

func cloneLive(live []ir.LiveVersion) []ir.LiveVersion {
	return append([]ir.LiveVersion(nil), live...)
}

// applyAll applies every migration in mts to schema in slice order,
// accumulating CommittedOps. Once an operation cannot be applied
// (ir.ErrUnclassifiedOperation), every subsequent state is marked
// Indeterminate rather than guessed at.
func applyAll(schema ir.Schema, committed []ir.CommittedOp, mts []ir.MigrationTiming) (ir.Schema, []ir.CommittedOp, bool) {
	indeterminate := false
	for _, mt := range mts {
		schema, committed, indeterminate = applyOne(schema, committed, indeterminate, mt)
	}
	return schema, committed, indeterminate
}

func applyOne(schema ir.Schema, committed []ir.CommittedOp, indeterminate bool, mt ir.MigrationTiming) (ir.Schema, []ir.CommittedOp, bool) {
	out := append([]ir.CommittedOp(nil), committed...)
	for _, op := range mt.Migration.Operations {
		cop := ir.CommittedOp{MigrationID: mt.Migration.ID, Op: op}
		if op.Kind == ir.OpAlterColumnType && !indeterminate {
			// Captured here, immediately before Apply, since this is the
			// one point in the pipeline that holds both the pre-op Schema
			// and the op together (docs/invariants.md RP-DB-003's
			// required "prior Column.Type" evidence).
			if t, ok := schema.Table(op.Table); ok {
				if c, ok := t.Column(op.Column); ok {
					cop.PriorType = c.Type
				}
			}
		}
		out = append(out, cop)
		if indeterminate {
			continue
		}
		next, err := schema.Apply(op)
		if err != nil {
			indeterminate = true
			continue
		}
		schema = next
	}
	return schema, out, indeterminate
}

// applyDuringSubset applies exactly the PhaseDuringRollout migrations
// whose bit is set in dv, in duringMigs slice order, on top of the
// already-committed base schema/trail.
func applyDuringSubset(base ir.Schema, baseCommitted []ir.CommittedOp, baseIndeterminate bool, duringMigs []ir.MigrationTiming, dv int) (ir.Schema, []ir.CommittedOp, bool) {
	schema := base
	committed := append([]ir.CommittedOp(nil), baseCommitted...)
	indeterminate := baseIndeterminate
	for k, mt := range duringMigs {
		if dv&(1<<k) == 0 {
			continue
		}
		schema, committed, indeterminate = applyOne(schema, committed, indeterminate, mt)
	}
	return schema, committed, indeterminate
}
