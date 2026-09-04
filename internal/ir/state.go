package ir

import "sort"

// LiveVersion records that one version of one named service is live
// (serving, or otherwise present in a way that makes its declared schema
// access reachable) in a RolloutState.
type LiveVersion struct {
	ServiceName string
	Version     string
}

func compareLiveVersions(a, b LiveVersion) int {
	if a.ServiceName != b.ServiceName {
		if a.ServiceName < b.ServiceName {
			return -1
		}
		return 1
	}
	if a.Version != b.Version {
		if a.Version < b.Version {
			return -1
		}
		return 1
	}
	return 0
}

// CommittedOp records one migration operation reflected in a
// SchemaState, tagged with its source migration for evidence citation.
type CommittedOp struct {
	MigrationID string
	Op          MigrationOp

	// PriorType is the column's declared type immediately before this
	// operation committed, populated only when Op.Kind == OpAlterColumnType
	// (docs/invariants.md RP-DB-003's "prior Column.Type" required
	// evidence). Empty when not applicable, or when it could not be
	// determined (e.g. an earlier unclassified operation left schema
	// state unknown from that point forward) — RP-DB-003 must treat an
	// empty PriorType as UNKNOWN, never as a license to skip the check.
	PriorType string
}

// SchemaState is a schema snapshot together with the trail of migration
// operations committed to reach it, in commit order. An empty
// CommittedOps means this is the plan's BaseSchema, with nothing applied
// yet.
//
// Indeterminate is true when at least one committed operation could not
// be applied (ir.OpUnclassified, docs/architecture.md §8) — Schema then
// reflects only the last point at which it was known, and any invariant
// evaluation against this state must treat schema-dependent facts as
// UNKNOWN, never assume the unapplied operation was a no-op
// (docs/vision.md §11).
type SchemaState struct {
	Schema        Schema
	CommittedOps  []CommittedOp
	Indeterminate bool
}

// EventKind names the mechanical event that moves a rollout from one
// RolloutState to the next (docs/architecture.md §4.1). The zero value,
// EventUnknown, must never appear on an edge actually added to a built
// graph — it exists only so a zero-valued TransitionEvent is visibly
// invalid rather than silently mistaken for a real event.
type EventKind int

const (
	EventUnknown EventKind = iota
	EventMigrationOpCommitted
	EventNewReplicaReady
	EventOldReplicaTerminated
)

func (k EventKind) String() string {
	switch k {
	case EventMigrationOpCommitted:
		return "MigrationOpCommitted"
	case EventNewReplicaReady:
		return "NewReplicaReady"
	case EventOldReplicaTerminated:
		return "OldReplicaTerminated"
	default:
		return "Unknown"
	}
}

// TransitionEvent is the edge label in the transition graph: what
// happened to move from one RolloutState to the next.
type TransitionEvent struct {
	Kind   EventKind
	Detail string
}

// RolloutState is a snapshot of "what versions of what are live, and what
// schema is committed" at one point reachable during a rollout
// (docs/architecture.md §3.1). ID is assigned by the graph builder in a
// fixed traversal order (docs/architecture.md §4.3) so it can serve as a
// stable, deterministic node identifier for diagnostics and tests.
type RolloutState struct {
	ID          int
	Live        []LiveVersion
	SchemaState SchemaState
}

// LiveVersionsFor returns the live versions of the given service in this
// state, sorted, deduplicated.
func (s RolloutState) LiveVersionsFor(serviceName string) []string {
	var out []string
	for _, lv := range s.Live {
		if lv.ServiceName == serviceName {
			out = append(out, lv.Version)
		}
	}
	sort.Strings(out)
	return out
}

// NewRolloutState constructs a RolloutState with its Live set
// deduplicated and sorted, so two states built from the same facts in a
// different order are equal under reflect.DeepEqual.
func NewRolloutState(id int, live []LiveVersion, schemaState SchemaState) RolloutState {
	seen := make(map[LiveVersion]struct{}, len(live))
	out := make([]LiveVersion, 0, len(live))
	for _, lv := range live {
		if _, ok := seen[lv]; ok {
			continue
		}
		seen[lv] = struct{}{}
		out = append(out, lv)
	}
	sort.Slice(out, func(i, j int) bool { return compareLiveVersions(out[i], out[j]) < 0 })
	return RolloutState{ID: id, Live: out, SchemaState: schemaState}
}
