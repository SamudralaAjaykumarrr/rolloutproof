// Package verify wires the full V1 pipeline together end to end: load a
// rollout plan and its contract metadata (internal/parser/rolloutplan,
// internal/parser/contract), build the transition graph
// (internal/graph), and evaluate the invariant catalog
// (internal/invariant). It exists as its own package, separate from
// cmd/rolloutproof, so that end-to-end tests (tests/) can exercise the
// real pipeline directly rather than shelling out to the compiled binary.
package verify

import (
	"os"
	"path/filepath"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/graph"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/invariant"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/parser/contract"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/parser/rolloutplan"
)

// Run loads and verifies the rollout plan described by dir (per
// internal/parser/rolloutplan's directory convention), returning one
// ir.Diagnostic per invariant internal/invariant implements. A non-nil
// error means verification could not run at all (a malformed or missing
// required file) — distinct from a returned UNKNOWN diagnostic, which is
// a real, positive verification result (docs/vision.md §10).
func Run(dir string) ([]ir.Diagnostic, error) {
	plan, err := rolloutplan.LoadDir(dir)
	if err != nil {
		return nil, err
	}

	services, contracts, err := loadServices(dir)
	if err != nil {
		return nil, err
	}

	g, err := graph.Build(plan)
	if err != nil {
		return nil, err
	}

	diags := invariant.Evaluate(plan, g, services)
	diags = append(diags, invariant.EvaluateRollbackPlan(plan, g, services)...)
	diags = append(diags, invariant.EvaluateAPI(g, services, contracts)...)
	diags = append(diags, invariant.EvaluateOrder(plan, g, services)...)
	// EvaluateK8s runs last: RP-K8S-001 derives its finding from every
	// other family's results (see EvaluateK8s's doc comment).
	diags = append(diags, invariant.EvaluateK8s(plan, g, diags)...)
	return diags, nil
}

// loadServices loads contract metadata from <dir>/contracts, if that
// directory exists. Its absence is not an error: every invariant that
// needs a live version's declared facts will report UNKNOWN, which is
// the correct, honest outcome (docs/vision.md §10), not a tool failure.
func loadServices(dir string) (map[ir.ServiceKey]ir.Service, map[ir.APIContractKey]ir.APIContract, error) {
	contractsDir := filepath.Join(dir, "contracts")
	if _, err := os.Stat(contractsDir); err != nil {
		return map[ir.ServiceKey]ir.Service{}, map[ir.APIContractKey]ir.APIContract{}, nil
	}
	reg, err := contract.LoadDir(contractsDir)
	if err != nil {
		return nil, nil, err
	}
	return reg.Services(), reg.APIContracts(), nil
}
