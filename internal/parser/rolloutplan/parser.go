// Package rolloutplan assembles a complete ir.RolloutPlan from a fixed
// directory convention (docs/architecture.md §8):
//
//	<dir>/deployment.yaml    one or more Kubernetes Deployment manifests
//	<dir>/schema.yaml        the schema the plan's migrations apply against
//	<dir>/rolloutplan.yaml   which workload changes version, to what, and
//	                         which migration files commit at which phase
//	<dir>/<migration files>  referenced by rolloutplan.yaml, resolved
//	                         relative to <dir>
//
// Contract metadata (conventionally <dir>/contracts/) is loaded
// separately by internal/parser/contract.LoadDir — this package has no
// opinion about service facts, only about rollout mechanics, keeping the
// dependency direction in docs/architecture.md §10 intact (this package
// depends on internal/parser/k8s and internal/parser/sql, not on
// internal/parser/contract).
//
// migration Phase is the single most safety-critical fact in this whole
// format (docs/architecture.md §3.3, assumption A2) and is always an
// explicit, authored field — never inferred from file order or timing.
package rolloutplan

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/parser/k8s"
	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/parser/sql"
)

// PlanAPIVersion is the only apiVersion V1 accepts for a rolloutplan.yaml.
const PlanAPIVersion = "rolloutproof.dev/v1alpha1"

// PlanKind is the required kind for a rolloutplan.yaml.
const PlanKind = "RolloutPlan"

type migrationEntry struct {
	File  string `yaml:"file"`
	Phase string `yaml:"phase"`
}

type rollbackEntry struct {
	ToVersion string `yaml:"toVersion"`
}

type planDoc struct {
	APIVersion  string           `yaml:"apiVersion"`
	Kind        string           `yaml:"kind"`
	Workload    string           `yaml:"workload"`
	FromVersion string           `yaml:"fromVersion"`
	ToVersion   string           `yaml:"toVersion"`
	Migrations  []migrationEntry `yaml:"migrations"`
	Rollback    *rollbackEntry   `yaml:"rollback"`
}

func parsePhase(filename, s string) (ir.MigrationPhase, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "before":
		return ir.PhaseBeforeRollout, nil
	case "during":
		return ir.PhaseDuringRollout, nil
	case "after":
		return ir.PhaseAfterRollout, nil
	default:
		return ir.PhaseUnknown, &ParseError{File: filename, Message: fmt.Sprintf(
			"unknown migration phase %q (expected \"before\", \"during\", or \"after\")", s)}
	}
}

// LoadDir assembles a full ir.RolloutPlan from dir. See the package doc
// for the directory convention.
func LoadDir(dir string) (ir.RolloutPlan, error) {
	planPath := filepath.Join(dir, "rolloutplan.yaml")
	planSrc, err := os.ReadFile(planPath)
	if err != nil {
		return ir.RolloutPlan{}, fmt.Errorf("rolloutplan: reading %s: %w", planPath, err)
	}

	dec := yaml.NewDecoder(bytes.NewReader(planSrc))
	dec.KnownFields(true)
	var doc planDoc
	if err := dec.Decode(&doc); err != nil {
		return ir.RolloutPlan{}, &ParseError{File: planPath, Message: err.Error()}
	}
	if doc.APIVersion != PlanAPIVersion {
		return ir.RolloutPlan{}, &ParseError{File: planPath, Message: fmt.Sprintf(
			"unsupported apiVersion %q (V1 supports only %q)", doc.APIVersion, PlanAPIVersion)}
	}
	if doc.Kind != PlanKind {
		return ir.RolloutPlan{}, &ParseError{File: planPath, Message: fmt.Sprintf(
			"unsupported kind %q (expected %q)", doc.Kind, PlanKind)}
	}
	if doc.Workload == "" || doc.FromVersion == "" || doc.ToVersion == "" {
		return ir.RolloutPlan{}, &ParseError{File: planPath, Message: "workload, fromVersion, and toVersion are all required"}
	}

	deploymentPath := filepath.Join(dir, "deployment.yaml")
	deploySrc, err := os.ReadFile(deploymentPath)
	if err != nil {
		return ir.RolloutPlan{}, fmt.Errorf("rolloutplan: reading %s: %w", deploymentPath, err)
	}
	workloads, _, err := k8s.ParseAll(deploySrc)
	if err != nil {
		return ir.RolloutPlan{}, err
	}
	var workload *ir.Workload
	for i := range workloads {
		if workloads[i].Name == doc.Workload {
			workload = &workloads[i]
			break
		}
	}
	if workload == nil {
		return ir.RolloutPlan{}, &ParseError{File: deploymentPath, Message: fmt.Sprintf(
			"no Deployment named %q found (rolloutplan.yaml names this as the changing workload)", doc.Workload)}
	}

	schemaPath := filepath.Join(dir, "schema.yaml")
	schemaSrc, err := os.ReadFile(schemaPath)
	if err != nil {
		return ir.RolloutPlan{}, fmt.Errorf("rolloutplan: reading %s: %w", schemaPath, err)
	}
	baseSchema, err := ParseSchema(schemaPath, schemaSrc)
	if err != nil {
		return ir.RolloutPlan{}, err
	}

	migrationTimings := make([]ir.MigrationTiming, 0, len(doc.Migrations))
	for _, me := range doc.Migrations {
		phase, err := parsePhase(planPath, me.Phase)
		if err != nil {
			return ir.RolloutPlan{}, err
		}
		migPath := filepath.Join(dir, me.File)
		migSrc, err := os.ReadFile(migPath)
		if err != nil {
			return ir.RolloutPlan{}, fmt.Errorf("rolloutplan: reading %s: %w", migPath, err)
		}
		mig, err := sql.Parse(me.File, string(migSrc))
		if err != nil {
			return ir.RolloutPlan{}, err
		}
		migrationTimings = append(migrationTimings, ir.MigrationTiming{Migration: mig, Phase: phase})
	}

	var rollbackTarget *ir.RollbackTarget
	if doc.Rollback != nil {
		rollbackTarget = &ir.RollbackTarget{Workload: *workload, ToVersion: doc.Rollback.ToVersion}
	}

	plan, err := ir.NewRolloutPlan(ir.RolloutPlan{
		BaseSchema:     baseSchema,
		Workloads:      []ir.WorkloadChange{{Workload: *workload, FromVersion: doc.FromVersion, ToVersion: doc.ToVersion}},
		Migrations:     migrationTimings,
		RollbackTarget: rollbackTarget,
	})
	if err != nil {
		return ir.RolloutPlan{}, fmt.Errorf("rolloutplan: %w", err)
	}
	return plan, nil
}
