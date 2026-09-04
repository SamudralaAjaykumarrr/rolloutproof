package rolloutplan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}
}

const deploymentYAML = `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
  labels:
    rolloutproof.dev/service: api
    rolloutproof.dev/version: v2
spec:
  replicas: 3
  strategy:
    type: RollingUpdate
    rollingUpdate: {maxSurge: 1, maxUnavailable: 0}
  selector: {matchLabels: {app: api}}
  template:
    metadata: {labels: {app: api}}
    spec:
      containers: [{name: api, image: "api:v2"}]
`

const schemaYAML = `
apiVersion: rolloutproof.dev/v1alpha1
kind: Schema
tables:
  - name: users
    columns:
      - name: id
        type: integer
      - name: email
        type: text
        nullable: true
`

const migrationSQL = `ALTER TABLE users DROP COLUMN email;`

func writeFullFixture(t *testing.T, dir, phase string) {
	t.Helper()
	writeFile(t, dir, "deployment.yaml", deploymentYAML)
	writeFile(t, dir, "schema.yaml", schemaYAML)
	writeFile(t, dir, "migration.sql", migrationSQL)
	writeFile(t, dir, "rolloutplan.yaml", `
apiVersion: rolloutproof.dev/v1alpha1
kind: RolloutPlan
workload: api
fromVersion: v1
toVersion: v2
migrations:
  - file: migration.sql
    phase: `+phase+`
`)
}

func TestLoadDir_Valid(t *testing.T) {
	dir := t.TempDir()
	writeFullFixture(t, dir, "during")

	plan, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Workloads) != 1 {
		t.Fatalf("expected 1 workload change, got %d", len(plan.Workloads))
	}
	if plan.Workloads[0].FromVersion != "v1" || plan.Workloads[0].ToVersion != "v2" {
		t.Fatalf("unexpected version transition: %+v", plan.Workloads[0])
	}
	if len(plan.Migrations) != 1 || plan.Migrations[0].Phase != ir.PhaseDuringRollout {
		t.Fatalf("unexpected migrations: %+v", plan.Migrations)
	}
	if !plan.BaseSchema.HasColumn(ir.ColumnRef{Table: "users", Column: "email"}) {
		t.Fatalf("expected base schema to include users.email")
	}
}

func TestLoadDir_AllPhases(t *testing.T) {
	for _, tc := range []struct {
		yaml string
		want ir.MigrationPhase
	}{
		{"before", ir.PhaseBeforeRollout},
		{"during", ir.PhaseDuringRollout},
		{"after", ir.PhaseAfterRollout},
	} {
		dir := t.TempDir()
		writeFullFixture(t, dir, tc.yaml)
		plan, err := LoadDir(dir)
		if err != nil {
			t.Fatalf("phase %q: unexpected error: %v", tc.yaml, err)
		}
		if plan.Migrations[0].Phase != tc.want {
			t.Fatalf("phase %q: expected %v, got %v", tc.yaml, tc.want, plan.Migrations[0].Phase)
		}
	}
}

func TestLoadDir_RejectsUnknownPhase(t *testing.T) {
	dir := t.TempDir()
	writeFullFixture(t, dir, "sometime")
	if _, err := LoadDir(dir); err == nil {
		t.Fatalf("expected error for unknown phase")
	}
}

func TestLoadDir_RejectsMissingWorkload(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "deployment.yaml", deploymentYAML)
	writeFile(t, dir, "schema.yaml", schemaYAML)
	writeFile(t, dir, "rolloutplan.yaml", `
apiVersion: rolloutproof.dev/v1alpha1
kind: RolloutPlan
workload: nonexistent
fromVersion: v1
toVersion: v2
`)
	if _, err := LoadDir(dir); err == nil {
		t.Fatalf("expected error when named workload is not found in deployment.yaml")
	}
}

func TestLoadDir_RejectsMissingRequiredFields(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "deployment.yaml", deploymentYAML)
	writeFile(t, dir, "schema.yaml", schemaYAML)
	writeFile(t, dir, "rolloutplan.yaml", `
apiVersion: rolloutproof.dev/v1alpha1
kind: RolloutPlan
workload: api
`)
	if _, err := LoadDir(dir); err == nil {
		t.Fatalf("expected error for missing fromVersion/toVersion")
	}
}

func TestLoadDir_WithRollback(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "deployment.yaml", deploymentYAML)
	writeFile(t, dir, "schema.yaml", schemaYAML)
	writeFile(t, dir, "migration.sql", migrationSQL)
	writeFile(t, dir, "rolloutplan.yaml", `
apiVersion: rolloutproof.dev/v1alpha1
kind: RolloutPlan
workload: api
fromVersion: v1
toVersion: v2
migrations:
  - file: migration.sql
    phase: before
rollback:
  toVersion: v1
`)
	plan, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.RollbackTarget == nil || plan.RollbackTarget.ToVersion != "v1" {
		t.Fatalf("expected rollback target v1, got %+v", plan.RollbackTarget)
	}
}

const secondDeploymentYAML = `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: checkout
  labels:
    rolloutproof.dev/service: checkout
    rolloutproof.dev/version: v1
spec:
  replicas: 1
  strategy: {type: Recreate}
  selector: {matchLabels: {app: checkout}}
  template:
    metadata: {labels: {app: checkout}}
    spec:
      containers: [{name: checkout, image: "checkout:v1"}]
`

func TestLoadDir_StaticWorkload(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "deployment.yaml", deploymentYAML+"\n---\n"+secondDeploymentYAML)
	writeFile(t, dir, "schema.yaml", schemaYAML)
	writeFile(t, dir, "migration.sql", migrationSQL)
	writeFile(t, dir, "rolloutplan.yaml", `
apiVersion: rolloutproof.dev/v1alpha1
kind: RolloutPlan
workload: api
fromVersion: v1
toVersion: v2
migrations:
  - file: migration.sql
    phase: during
staticWorkloads:
  - checkout
`)
	plan, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Workloads) != 2 {
		t.Fatalf("expected 2 workload changes (1 changing + 1 static), got %d: %+v", len(plan.Workloads), plan.Workloads)
	}
	var found bool
	for _, wc := range plan.Workloads {
		if wc.Workload.Name == "checkout" {
			found = true
			if wc.FromVersion != "v1" || wc.ToVersion != "v1" {
				t.Fatalf("expected checkout to be a no-op transition at its declared version, got %+v", wc)
			}
		}
	}
	if !found {
		t.Fatalf("expected checkout to appear among plan.Workloads")
	}
}

func TestLoadDir_RejectsUnknownStaticWorkload(t *testing.T) {
	dir := t.TempDir()
	writeFullFixture(t, dir, "during")
	// Overwrite rolloutplan.yaml with a staticWorkloads entry naming a
	// Deployment that doesn't exist in deployment.yaml.
	writeFile(t, dir, "rolloutplan.yaml", `
apiVersion: rolloutproof.dev/v1alpha1
kind: RolloutPlan
workload: api
fromVersion: v1
toVersion: v2
migrations:
  - file: migration.sql
    phase: during
staticWorkloads:
  - nonexistent
`)
	if _, err := LoadDir(dir); err == nil {
		t.Fatalf("expected an error for a staticWorkloads entry with no matching Deployment")
	}
}

func TestParseSchema_RejectsUnknownField(t *testing.T) {
	_, err := ParseSchema("bad.yaml", []byte(`
apiVersion: rolloutproof.dev/v1alpha1
kind: Schema
tabels: []
`))
	if err == nil {
		t.Fatalf("expected error for unknown field 'tabels'")
	}
}

func TestParseSchema_DefaultsNullableFalse(t *testing.T) {
	schema, err := ParseSchema("s.yaml", []byte(`
apiVersion: rolloutproof.dev/v1alpha1
kind: Schema
tables:
  - name: users
    columns:
      - name: id
        type: integer
`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tab, _ := schema.Table("users")
	col, _ := tab.Column("id")
	if col.Nullable {
		t.Fatalf("expected nullable to default to false")
	}
}
