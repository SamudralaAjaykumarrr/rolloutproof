package invariant

import (
	"fmt"
	"testing"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/graph"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
)

// genAPIOrderPlan builds a plan exercising RP-API/RP-ORDER specifically:
// two services, "front" (the workload actually transitioning v1->v2) and
// "back" (a static dependency workload, FromVersion == ToVersion, per
// internal/parser/rolloutplan's staticWorkloads convention for
// non-transitioning workloads multi-service invariants need) — "back"
// itself has two declared versions in the services map even though only
// one is ever live, so a consumer can validly reference either.
func genAPIOrderPlan(c *cursor) (ir.RolloutPlan, map[ir.ServiceKey]ir.Service, map[ir.APIContractKey]ir.APIContract, bool) {
	frontWL, err := ir.NewWorkload(ir.Workload{
		Name: "front", ServiceName: "front", Version: "v2", Replicas: 1 + c.intn(3),
		Strategy: ir.RolloutStrategy{Type: ir.StrategyRollingUpdate, MaxSurge: ir.IntOrPercent{IntValue: 1 + c.intn(2)}},
	})
	if err != nil {
		return ir.RolloutPlan{}, nil, nil, false
	}
	backVersion := fmt.Sprintf("v%d", 1+c.intn(3))
	backWL, err := ir.NewWorkload(ir.Workload{
		Name: "back", ServiceName: "back", Version: backVersion, Replicas: 1,
		Strategy: ir.RolloutStrategy{Type: ir.StrategyRecreate},
	})
	if err != nil {
		return ir.RolloutPlan{}, nil, nil, false
	}

	table, err := ir.NewTable("t", []ir.Column{{Name: "x", Type: "integer", Nullable: true}})
	if err != nil {
		return ir.RolloutPlan{}, nil, nil, false
	}
	baseSchema, err := ir.NewSchema([]ir.Table{table})
	if err != nil {
		return ir.RolloutPlan{}, nil, nil, false
	}

	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema: baseSchema,
		Workloads: []ir.WorkloadChange{
			{Workload: frontWL, FromVersion: "v1", ToVersion: "v2"},
			{Workload: backWL, FromVersion: backVersion, ToVersion: backVersion},
		},
	})
	if err != nil {
		return ir.RolloutPlan{}, nil, nil, false
	}

	// Contract: "back" declares a single endpoint whose request/response
	// field requiredness is randomized per back-version.
	contracts := make(map[ir.APIContractKey]ir.APIContract)
	backVersions := []string{"v1", "v2", "v3"}
	requiredFieldByVersion := make(map[string]bool, len(backVersions))
	for _, v := range backVersions {
		reqRequired := c.bool()
		requiredFieldByVersion[v] = reqRequired
		reqShape, err := ir.NewShape([]ir.Field{{Name: "id", Required: reqRequired}})
		if err != nil {
			return ir.RolloutPlan{}, nil, nil, false
		}
		respHasField := c.bool()
		var respFields []ir.Field
		if respHasField {
			respFields = []ir.Field{{Name: "result", Required: false}}
		}
		respShape, err := ir.NewShape(respFields)
		if err != nil {
			return ir.RolloutPlan{}, nil, nil, false
		}
		contract, err := ir.NewAPIContract("backapi", "back", v, []ir.Endpoint{{Operation: "GET /x", RequestShape: reqShape, ResponseShape: respShape}})
		if err != nil {
			return ir.RolloutPlan{}, nil, nil, false
		}
		contracts[contract.Key()] = contract
	}

	// Consumer ("front", both v1 and v2) declares whether it sends the
	// request field and whether it requires the response field.
	services := make(map[ir.ServiceKey]ir.Service, 4)
	for _, v := range []string{"v1", "v2"} {
		var sent []string
		if c.bool() {
			sent = []string{"id"}
		}
		var required []string
		if c.bool() {
			required = []string{"result"}
		}
		var dependsOn []ir.ServiceDependency
		if c.bool() {
			minVer := backVersions[c.intn(len(backVersions))]
			dependsOn = []ir.ServiceDependency{{ServiceName: "back", MinCompatibleVersion: minVer}}
		}
		svc, err := ir.NewServiceWithAPI("front", v, nil, nil, dependsOn,
			nil, []ir.APIConsumption{{ContractName: "backapi", Operation: "GET /x", RequestFieldsSent: sent, RequiredResponseFields: required}})
		if err != nil {
			return ir.RolloutPlan{}, nil, nil, false
		}
		services[svc.Key()] = svc
	}
	for _, v := range backVersions {
		svc, err := ir.NewServiceWithAPI("back", v, nil, nil, nil, []ir.APIProvision{{ContractName: "backapi"}}, nil)
		if err != nil {
			return ir.RolloutPlan{}, nil, nil, false
		}
		services[svc.Key()] = svc
	}

	return plan, services, contracts, true
}

// oracleAPIOrderHazard independently re-derives RP-API-001/002/003 and
// RP-ORDER-001/002's hazards directly from the same services/contracts
// facts: for every live consumer, every live provider matching a
// consumed contract, check the endpoint/request/response facts by hand
// (not by calling into rpapi.go's own violation-collection code), and
// separately check every live consumer's version-constrained dependency
// against whichever provider version is actually live.
func oracleAPIOrderHazard(g *graph.Graph, services map[ir.ServiceKey]ir.Service, contracts map[ir.APIContractKey]ir.APIContract) string {
	for _, s := range g.Nodes {
		for _, clv := range s.Live {
			consumer, ok := services[ir.ServiceKey{Name: clv.ServiceName, Version: clv.Version}]
			if !ok {
				continue
			}
			for _, cons := range consumer.APIConsumes() {
				for _, plv := range s.Live {
					contract, ok := contracts[ir.APIContractKey{Name: cons.ContractName, ProviderService: plv.ServiceName, ProviderVersion: plv.Version}]
					if !ok {
						continue
					}
					ep, ok := contract.Endpoint(cons.Operation)
					if !ok {
						return fmt.Sprintf("state %d: %s@%s calls %s on contract %s, which %s@%s no longer provides", s.ID, clv.ServiceName, clv.Version, cons.Operation, cons.ContractName, plv.ServiceName, plv.Version)
					}
					for _, f := range ep.RequestShape.Fields() {
						if f.Required && !containsStringOracle(cons.RequestFieldsSent, f.Name) {
							return fmt.Sprintf("state %d: %s@%s omits required request field %s on %s", s.ID, clv.ServiceName, clv.Version, f.Name, cons.Operation)
						}
					}
					for _, f := range cons.RequiredResponseFields {
						if !ep.ResponseShape.HasField(f) {
							return fmt.Sprintf("state %d: %s@%s requires response field %s on %s, which %s@%s's response no longer includes", s.ID, clv.ServiceName, clv.Version, f, cons.Operation, plv.ServiceName, plv.Version)
						}
					}
				}
			}
			for _, dep := range consumer.DependsOn() {
				if dep.MinCompatibleVersion == "" {
					continue
				}
				for _, plv := range s.Live {
					if plv.ServiceName != dep.ServiceName {
						continue
					}
					cmp, ok := compareVersions(plv.Version, dep.MinCompatibleVersion)
					if ok && cmp < 0 {
						return fmt.Sprintf("state %d: %s@%s requires %s>=%s, but %s@%s is live", s.ID, clv.ServiceName, clv.Version, dep.ServiceName, dep.MinCompatibleVersion, plv.ServiceName, plv.Version)
					}
				}
			}
		}
	}
	return ""
}

func containsStringOracle(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func FuzzAdversarialAPIOrder(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{1, 0, 1, 1, 0, 1, 0, 1, 1, 0})
	f.Add(make([]byte, 32))

	f.Fuzz(func(t *testing.T, data []byte) {
		c := &cursor{data: data}
		plan, services, contracts, ok := genAPIOrderPlan(c)
		if !ok {
			t.Skip("cursor exhausted before a well-formed plan could be built")
		}
		g, diags, overall, err := runFullPipeline(plan, services, contracts)
		if err != nil {
			t.Skipf("plan rejected by the real pipeline: %v", err)
		}
		if hazard := oracleAPIOrderHazard(g, services, contracts); hazard != "" && overall == ir.VerdictSafe {
			t.Fatalf("FALSE SAFE (API/Order): engine reported overall SAFE, but a hazard is independently derivable:\n  %s\ndiagnostics: %+v", hazard, diags)
		}
	})
}
