package invariant

import (
	"fmt"
	"testing"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/graph"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/parser/sql"
)

// This file is the "false-SAFE attack harness": rather than checking the
// engine's output against a hand-picked expected verdict (every other
// test file in this package), it generates a wide variety of *valid*
// RolloutPlans directly from semantic IR facts — random schemas, random
// migrations (parsed through the real SQL parser, so Destructiveness/
// Reversibility classification is never reinvented here), random
// versions, strategies, and declared service/API/order facts — and
// checks a small set of properties that must hold for ANY valid plan,
// independent of which specific invariant is responsible for enforcing
// them. A property violation means: a reachable state exists where a
// hazard this project's own semantic model recognizes is present, yet
// the real engine's aggregate verdict is SAFE. That is the specific
// defect class docs/ADVERSARIAL_REVIEW.md calls "false SAFE" and this
// project treats as the single most serious kind of bug it can have.
//
// The oracle checks below deliberately duplicate none of the invariant
// packages' own bookkeeping (violation types, gapSet, counterexample
// selection) — they re-derive each hazard from first principles against
// the same ir.RolloutPlan/graph.Graph/services facts fed to the real
// engine, so a bug in the engine's own violation-collection logic cannot
// also be present in the oracle checking it.
//
// A discovered false-SAFE becomes a permanent fuzz corpus entry
// (testdata/fuzz/FuzzAdversarialRollout/...) the moment go test -fuzz
// finds one and is interrupted — every future `go test ./...` replays it
// as an ordinary regression case.

type cursor struct {
	data []byte
	pos  int
}

func (c *cursor) next() byte {
	if c.pos >= len(c.data) {
		return 0
	}
	b := c.data[c.pos]
	c.pos++
	return b
}

func (c *cursor) intn(n int) int {
	if n <= 0 {
		return 0
	}
	return int(c.next()) % n
}

func (c *cursor) bool() bool { return c.next()&1 == 1 }

var fuzzColumnTypes = []string{"integer", "bigint", "smallint", "text", "varchar(5)", "varchar(50)"}

// genPlan builds one candidate RolloutPlan plus its services/contracts
// map from a byte cursor, generating migrations as raw SQL text and
// parsing it through the real internal/parser/sql.Parse — the same
// classification path a real migrations/ directory goes through — so a
// generated MigrationOp's Destructiveness/Reversibility is never guessed
// at here. Returns ok=false when the cursor ran out of meaningful choices
// early enough that continuing would just replay the zero value
// repeatedly (not an error — the caller skips the case).
func genPlan(c *cursor) (ir.RolloutPlan, map[ir.ServiceKey]ir.Service, map[ir.APIContractKey]ir.APIContract, bool) {
	colNames := []string{"a", "b"}
	colTypes := make(map[string]string, 2)
	colNullable := make(map[string]bool, 2)
	var cols []ir.Column
	for _, name := range colNames {
		typ := fuzzColumnTypes[c.intn(len(fuzzColumnTypes))]
		nullable := c.bool()
		var def *string
		if c.bool() {
			d := "0"
			def = &d
		}
		colTypes[name] = typ
		colNullable[name] = nullable
		cols = append(cols, ir.Column{Name: name, Type: typ, Nullable: nullable, Default: def})
	}
	table, err := ir.NewTable("t", cols)
	if err != nil {
		return ir.RolloutPlan{}, nil, nil, false
	}
	baseSchema, err := ir.NewSchema([]ir.Table{table})
	if err != nil {
		return ir.RolloutPlan{}, nil, nil, false
	}

	// 0-2 migrations, each one randomly-chosen statement against an
	// existing column (or a new one, for ADD COLUMN), phased randomly.
	numMigs := c.intn(3)
	var migTimings []ir.MigrationTiming
	phases := []ir.MigrationPhase{ir.PhaseBeforeRollout, ir.PhaseDuringRollout, ir.PhaseAfterRollout}
	for i := 0; i < numMigs; i++ {
		target := colNames[c.intn(len(colNames))]
		var stmt string
		switch c.intn(6) {
		case 0:
			stmt = fmt.Sprintf("ALTER TABLE t ADD COLUMN newcol%d %s;", i, fuzzColumnTypes[c.intn(len(fuzzColumnTypes))])
		case 1:
			stmt = fmt.Sprintf("ALTER TABLE t DROP COLUMN %s;", target)
		case 2:
			stmt = fmt.Sprintf("ALTER TABLE t RENAME COLUMN %s TO %s_renamed;", target, target)
		case 3:
			stmt = fmt.Sprintf("ALTER TABLE t ALTER COLUMN %s TYPE %s;", target, fuzzColumnTypes[c.intn(len(fuzzColumnTypes))])
		case 4:
			stmt = fmt.Sprintf("ALTER TABLE t ALTER COLUMN %s SET NOT NULL;", target)
		case 5:
			stmt = fmt.Sprintf("ALTER TABLE t ALTER COLUMN %s DROP NOT NULL;", target)
		}
		mig, err := sql.Parse(fmt.Sprintf("%03d_mig.sql", i), stmt)
		if err != nil {
			return ir.RolloutPlan{}, nil, nil, false
		}
		migTimings = append(migTimings, ir.MigrationTiming{Migration: mig, Phase: phases[c.intn(len(phases))]})
	}

	replicas := 1 + c.intn(4)
	var strategy ir.RolloutStrategy
	if c.bool() {
		strategy = ir.RolloutStrategy{Type: ir.StrategyRecreate}
	} else {
		strategy = ir.RolloutStrategy{
			Type:           ir.StrategyRollingUpdate,
			MaxSurge:       ir.IntOrPercent{IntValue: c.intn(3)},
			MaxUnavailable: ir.IntOrPercent{IntValue: c.intn(3)},
		}
	}
	wl, err := ir.NewWorkload(ir.Workload{
		Name: "svc", ServiceName: "svc", Version: "v2", Replicas: replicas, Strategy: strategy,
	})
	if err != nil {
		return ir.RolloutPlan{}, nil, nil, false
	}

	planBuilder := ir.RolloutPlan{
		BaseSchema: baseSchema,
		Workloads:  []ir.WorkloadChange{{Workload: wl, FromVersion: "v1", ToVersion: "v2"}},
		Migrations: migTimings,
	}
	if c.bool() {
		planBuilder.RollbackTarget = &ir.RollbackTarget{Workload: wl, ToVersion: "v1"}
	}
	plan, err := ir.NewRolloutPlan(planBuilder)
	if err != nil {
		return ir.RolloutPlan{}, nil, nil, false
	}

	// Random declared facts for v1/v2: each independently may read/write
	// each column (including ones not (yet) in the schema — a valid,
	// meaningful fact: "this version depends on a column no migration in
	// this plan has added").
	services := make(map[ir.ServiceKey]ir.Service, 2)
	allCols := append(append([]string(nil), colNames...), "newcol0", "newcol1")
	for _, v := range []string{"v1", "v2"} {
		var reads, writes []ir.ColumnRef
		for _, col := range allCols {
			if c.bool() {
				reads = append(reads, ir.ColumnRef{Table: "t", Column: col})
			}
			if c.bool() {
				writes = append(writes, ir.ColumnRef{Table: "t", Column: col})
			}
		}
		svc, err := ir.NewService("svc", v, reads, writes, nil)
		if err != nil {
			return ir.RolloutPlan{}, nil, nil, false
		}
		services[svc.Key()] = svc
	}

	return plan, services, map[ir.APIContractKey]ir.APIContract{}, true
}

// oracleHazards independently re-derives, from the same facts fed to the
// real engine, every reachable-state hazard this fuzz harness knows how
// to check: a live version's declared read/write column absent from the
// schema (RP-DB-001/002's territory, including via drop/rename), a live
// version writing a table without covering a NOT NULL/no-default column
// (RP-DB-004's territory), and a live version reading/writing a column
// whose type was changed non-widening (RP-DB-003's territory, including —
// critically — via a rollback graph's carried-forward history). Returns a
// human-readable description of the first hazard found, or "" if none.
//
// skipVacuous mirrors evaluateColumnFamily's own vacuousWhenNoOp
// parameter (rpdb.go) and docs/scenario-corpus.md SC-SAFE-004's
// deliberate, documented scope boundary: a hazard already present in g's
// own BaseSchema, unrelated to anything g's plan actually committed, is
// out of RP-DB-001/002/004's stated mission (docs/invariants.md RP-DB-004:
// NOT NULL *introduction*, not a general audit of an unchanging schema) —
// if it were a real problem, the live version would already be failing in
// production today, unrelated to this rollout. This is why the check is
// skipVacuous rather than "always skip pre-existing conditions": the
// caller passes false for a rollback graph, matching evaluateColumnFamily's
// own vacuousWhenNoOp=false there (rollback_plan.go) — a rollback graph
// also commits no migrations of its own by design, but its BaseSchema is
// whatever the *forward* plan actually changed, which is exactly the kind
// of real, rollout-caused difference this skip must not suppress.
func oracleHazards(g *graph.Graph, services map[ir.ServiceKey]ir.Service, skipVacuous bool) string {
	if skipVacuous && !graphHasAnyCommittedOp(g) {
		return ""
	}
	for _, s := range g.Nodes {
		if s.SchemaState.Indeterminate {
			continue // no ground truth derivable; the engine's own UNKNOWN handling is this harness's only expectation here, not asserted further
		}
		for _, lv := range s.Live {
			svc, ok := services[ir.ServiceKey{Name: lv.ServiceName, Version: lv.Version}]
			if !ok {
				continue // missing evidence; engine must report UNKNOWN, not this harness's concern (asserted separately below)
			}
			for _, col := range append(svc.SchemaReads(), svc.SchemaWrites()...) {
				if !s.SchemaState.Schema.HasColumn(col) {
					return fmt.Sprintf("state %d: %s@%s declares access to %s, which does not exist in the committed schema", s.ID, lv.ServiceName, lv.Version, col)
				}
			}
		}
		for _, table := range s.SchemaState.Schema.Tables() {
			for _, col := range table.Columns() {
				if col.Nullable || col.Default != nil {
					continue
				}
				ref := ir.ColumnRef{Table: table.Name(), Column: col.Name}
				for _, lv := range s.Live {
					svc, ok := services[ir.ServiceKey{Name: lv.ServiceName, Version: lv.Version}]
					if !ok || !svc.TouchesTable(ref.Table) || !svc.WritesTable(ref.Table) {
						continue
					}
					if !svc.WritesColumn(ref) {
						return fmt.Sprintf("state %d: %s@%s writes table %s without covering NOT NULL column %s", s.ID, lv.ServiceName, lv.Version, ref.Table, ref.Column)
					}
				}
			}
		}
		for _, cop := range s.SchemaState.CommittedOps {
			if cop.Op.Kind != ir.OpAlterColumnType || cop.PriorType == "" {
				continue
			}
			if classifyTypeChange(cop.PriorType, cop.Op.NewType) == typeWidening {
				continue
			}
			target := cop.Op.TargetColumn()
			for _, lv := range s.Live {
				svc, ok := services[ir.ServiceKey{Name: lv.ServiceName, Version: lv.Version}]
				if !ok {
					continue
				}
				if svc.ReadsColumn(target) || svc.WritesColumn(target) {
					return fmt.Sprintf("state %d: %s@%s reads/writes %s, whose type non-widening-changed from %s to %s (migration %q)", s.ID, lv.ServiceName, lv.Version, target, cop.PriorType, cop.Op.NewType, cop.MigrationID)
				}
			}
		}
	}
	return ""
}

// runFullPipeline mirrors internal/verify.Run's exact orchestration
// (graph.Build -> Evaluate -> EvaluateRollbackPlan -> EvaluateAPI ->
// EvaluateOrder -> EvaluateK8s -> Aggregate), duplicated here rather than
// imported because internal/verify itself depends on parser packages this
// harness has no reason to exercise — the orchestration order is the only
// part that matters for this file's properties, and it must track
// verify.Run's if that ever changes.
func runFullPipeline(plan ir.RolloutPlan, services map[ir.ServiceKey]ir.Service, contracts map[ir.APIContractKey]ir.APIContract) (*graph.Graph, []ir.Diagnostic, ir.Verdict, error) {
	g, err := graph.Build(plan)
	if err != nil {
		return nil, nil, ir.VerdictUnknown, err
	}
	diags := Evaluate(plan, g, services)
	diags = append(diags, EvaluateRollbackPlan(plan, g, services)...)
	diags = append(diags, EvaluateAPI(g, services, contracts)...)
	diags = append(diags, EvaluateOrder(plan, g, services)...)
	diags = append(diags, EvaluateK8s(plan, g, diags)...)
	return g, diags, Aggregate(diags), nil
}

func FuzzAdversarialRollout(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{1, 2, 3, 4, 5, 6, 7, 8})
	f.Add([]byte{0xFF, 0x00, 0xAB, 0xCD, 0x12, 0x34, 0x01, 0x00, 0x01, 0x00, 0x01})
	f.Add(make([]byte, 64)) // all-zero: every choice takes its first branch

	f.Fuzz(func(t *testing.T, data []byte) {
		c := &cursor{data: data}
		plan, services, contracts, ok := genPlan(c)
		if !ok {
			t.Skip("cursor exhausted before a well-formed plan could be built")
		}

		g, diags, overall, err := runFullPipeline(plan, services, contracts)
		if err != nil {
			t.Skipf("plan rejected by the real pipeline (not this harness's concern): %v", err)
		}

		if hazard := oracleHazards(g, services, true); hazard != "" && overall == ir.VerdictSafe {
			t.Fatalf("FALSE SAFE: engine reported overall SAFE, but a hazard is independently derivable from the same facts:\n  %s\ndiagnostics: %+v", hazard, diags)
		}

		// Same check against the rollback graph, when declared: a
		// rollback-side hazard must not leave the *overall* verdict SAFE
		// either (this is the exact bug class fixed by carrying forward's
		// CommittedOps into graph.BuildRollback's baseline). skipVacuous is
		// false here, matching evaluateColumnFamily's own vacuousWhenNoOp=false
		// for the rollback path (see oracleHazards's doc comment).
		if plan.RollbackTarget != nil {
			_, rbGraph, err := graph.BuildRollback(plan, g)
			if err == nil {
				if hazard := oracleHazards(rbGraph, services, false); hazard != "" && overall == ir.VerdictSafe {
					t.Fatalf("FALSE SAFE (rollback graph): engine reported overall SAFE, but a hazard is independently derivable from the rollback's own reachable states:\n  %s\ndiagnostics: %+v", hazard, diags)
				}
			}
		}
	})
}
