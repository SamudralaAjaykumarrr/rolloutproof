package contract

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
)

const validDoc = `
apiVersion: rolloutproof.dev/v1alpha1
kind: ServiceContract
service: api
version: v1
schema:
  reads:
    - users.email
    - users.id
  writes:
    - users.name
dependencies:
  - payments
  - service: notifications
    minCompatibleVersion: v2
`

func TestParse_Valid(t *testing.T) {
	svc, _, err := Parse("api-v1.yaml", []byte(validDoc))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if svc.Name != "api" || svc.Version != "v1" {
		t.Fatalf("unexpected identity: %+v", svc)
	}
	if !svc.ReadsColumn(ir.ColumnRef{Table: "users", Column: "email"}) {
		t.Fatalf("expected users.email to be a declared read")
	}
	if !svc.WritesColumn(ir.ColumnRef{Table: "users", Column: "name"}) {
		t.Fatalf("expected users.name to be a declared write")
	}
	deps := svc.DependsOn()
	if len(deps) != 2 {
		t.Fatalf("expected 2 dependencies, got %+v", deps)
	}
	foundBare, foundVersioned := false, false
	for _, d := range deps {
		if d.ServiceName == "payments" && d.MinCompatibleVersion == "" {
			foundBare = true
		}
		if d.ServiceName == "notifications" && d.MinCompatibleVersion == "v2" {
			foundVersioned = true
		}
	}
	if !foundBare || !foundVersioned {
		t.Fatalf("expected both dependency forms to parse correctly: %+v", deps)
	}
}

func TestParse_EmptySchemaBlock_IsValidClosedWorldEmpty(t *testing.T) {
	doc := `
apiVersion: rolloutproof.dev/v1alpha1
kind: ServiceContract
service: api
version: v1
`
	svc, _, err := Parse("api-v1.yaml", []byte(doc))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(svc.SchemaReads()) != 0 || len(svc.SchemaWrites()) != 0 {
		t.Fatalf("expected empty reads/writes, got %+v / %+v", svc.SchemaReads(), svc.SchemaWrites())
	}
}

func TestParse_RejectsWrongAPIVersion(t *testing.T) {
	doc := `
apiVersion: rolloutproof.dev/v2
kind: ServiceContract
service: api
version: v1
`
	_, _, err := Parse("bad.yaml", []byte(doc))
	if err == nil {
		t.Fatalf("expected error for unsupported apiVersion")
	}
}

func TestParse_RejectsWrongKind(t *testing.T) {
	doc := `
apiVersion: rolloutproof.dev/v1alpha1
kind: APIContract
service: api
version: v1
`
	_, _, err := Parse("bad.yaml", []byte(doc))
	if err == nil {
		t.Fatalf("expected error for unsupported kind")
	}
}

func TestParse_RejectsMissingService(t *testing.T) {
	doc := `
apiVersion: rolloutproof.dev/v1alpha1
kind: ServiceContract
version: v1
`
	_, _, err := Parse("bad.yaml", []byte(doc))
	if err == nil {
		t.Fatalf("expected error for missing service")
	}
}

func TestParse_RejectsMissingVersion(t *testing.T) {
	doc := `
apiVersion: rolloutproof.dev/v1alpha1
kind: ServiceContract
service: api
`
	_, _, err := Parse("bad.yaml", []byte(doc))
	if err == nil {
		t.Fatalf("expected error for missing version")
	}
}

func TestParse_RejectsUnknownTopLevelField(t *testing.T) {
	doc := `
apiVersion: rolloutproof.dev/v1alpha1
kind: ServiceContract
service: api
version: v1
shema:
  reads:
    - users.email
`
	_, _, err := Parse("typo.yaml", []byte(doc))
	if err == nil {
		t.Fatalf("expected error for unknown top-level field 'shema'")
	}
}

func TestParse_RejectsUnknownNestedField(t *testing.T) {
	doc := `
apiVersion: rolloutproof.dev/v1alpha1
kind: ServiceContract
service: api
version: v1
schema:
  reads:
    - users.email
  reeds:
    - users.name
`
	_, _, err := Parse("typo.yaml", []byte(doc))
	if err == nil {
		t.Fatalf("expected error for unknown nested field 'reeds'")
	}
}

func TestParse_RejectsUnknownDependencyField(t *testing.T) {
	doc := `
apiVersion: rolloutproof.dev/v1alpha1
kind: ServiceContract
service: api
version: v1
dependencies:
  - service: payments
    minCompatVersion: v3
`
	_, _, err := Parse("typo.yaml", []byte(doc))
	if err == nil {
		t.Fatalf("expected error for unknown dependency field 'minCompatVersion'")
	}
}

func TestParse_RejectsMalformedColumnReference(t *testing.T) {
	cases := []string{"usersemail", "users.email.extra", "."}
	for _, c := range cases {
		doc := "apiVersion: rolloutproof.dev/v1alpha1\nkind: ServiceContract\nservice: api\nversion: v1\nschema:\n  reads:\n    - " + c + "\n"
		if _, _, err := Parse("bad.yaml", []byte(doc)); err == nil {
			t.Errorf("expected error for malformed column reference %q", c)
		}
	}
}

func TestParse_DuplicateColumnRefsAreDeduplicated(t *testing.T) {
	doc := `
apiVersion: rolloutproof.dev/v1alpha1
kind: ServiceContract
service: api
version: v1
schema:
  reads:
    - users.email
    - users.email
`
	svc, _, err := Parse("dup.yaml", []byte(doc))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(svc.SchemaReads()) != 1 {
		t.Fatalf("expected deduplication, got %+v", svc.SchemaReads())
	}
}

func TestParse_DependencyMissingServiceInMapping(t *testing.T) {
	doc := `
apiVersion: rolloutproof.dev/v1alpha1
kind: ServiceContract
service: api
version: v1
dependencies:
  - minCompatibleVersion: v3
`
	_, _, err := Parse("bad.yaml", []byte(doc))
	if err == nil {
		t.Fatalf("expected error for a dependency mapping missing 'service'")
	}
}

func TestParse_MalformedYAML(t *testing.T) {
	_, _, err := Parse("bad.yaml", []byte("not: valid: yaml: [["))
	if err == nil {
		t.Fatalf("expected error for malformed YAML")
	}
}

func TestLoadDir_MultipleVersionsOfSameService(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "api-v1.yaml", `
apiVersion: rolloutproof.dev/v1alpha1
kind: ServiceContract
service: api
version: v1
schema:
  reads: [users.email]
`)
	writeFile(t, dir, "api-v2.yaml", `
apiVersion: rolloutproof.dev/v1alpha1
kind: ServiceContract
service: api
version: v2
`)
	reg, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	v1, ok := reg.Lookup("api", "v1")
	if !ok || !v1.ReadsColumn(ir.ColumnRef{Table: "users", Column: "email"}) {
		t.Fatalf("expected api@v1 to be loaded with its declared read")
	}
	v2, ok := reg.Lookup("api", "v2")
	if !ok || len(v2.SchemaReads()) != 0 {
		t.Fatalf("expected api@v2 to be loaded with no declared reads")
	}
	if _, ok := reg.Lookup("api", "v3"); ok {
		t.Fatalf("expected lookup miss for a version with no contract file")
	}
}

func TestLoadDir_DuplicateServiceVersionIsError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.yaml", `
apiVersion: rolloutproof.dev/v1alpha1
kind: ServiceContract
service: api
version: v1
`)
	writeFile(t, dir, "b.yaml", `
apiVersion: rolloutproof.dev/v1alpha1
kind: ServiceContract
service: api
version: v1
`)
	if _, err := LoadDir(dir); err == nil {
		t.Fatalf("expected error for duplicate (service, version) across files")
	}
}

func TestLoadDir_IgnoresNonYAMLFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "api-v1.yaml", `
apiVersion: rolloutproof.dev/v1alpha1
kind: ServiceContract
service: api
version: v1
`)
	writeFile(t, dir, "README.md", "not a contract file")
	reg, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := reg.Lookup("api", "v1"); !ok {
		t.Fatalf("expected api@v1 to load")
	}
}

func TestLoadDir_UnknownReferenceIsNotAParseError(t *testing.T) {
	// A dependency naming a service with no contract file of its own is
	// valid at parse time (docs/adr/0007) — cross-referencing is an
	// invariant-evaluation-time concern, not a loader concern.
	dir := t.TempDir()
	writeFile(t, dir, "checkout-v1.yaml", `
apiVersion: rolloutproof.dev/v1alpha1
kind: ServiceContract
service: checkout
version: v1
dependencies:
  - nonexistent-service
`)
	reg, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	svc, ok := reg.Lookup("checkout", "v1")
	if !ok {
		t.Fatalf("expected checkout@v1 to load")
	}
	if len(svc.DependsOn()) != 1 || svc.DependsOn()[0].ServiceName != "nonexistent-service" {
		t.Fatalf("unexpected dependencies: %+v", svc.DependsOn())
	}
}

func TestParse_APIBlockRequiresBetaVersion(t *testing.T) {
	doc := `
apiVersion: rolloutproof.dev/v1alpha1
kind: ServiceContract
service: orders
version: v2
api:
  provides:
    - contract: orders-api
`
	_, _, err := Parse("bad.yaml", []byte(doc))
	if err == nil {
		t.Fatalf("expected an error for an api block under v1alpha1")
	}
}

func TestParse_ProvidesAndConsumes(t *testing.T) {
	doc := `
apiVersion: rolloutproof.dev/v1beta1
kind: ServiceContract
service: orders
version: v2
api:
  provides:
    - contract: orders-api
      endpoints:
        - operation: "GET /orders/:id"
          request:
            fields:
              - name: id
                required: true
          response:
            fields:
              - name: id
                required: true
              - name: total_amount
                required: true
  consumes:
    - contract: payments-api
      operation: "POST /charges"
      requestFieldsSent: [amount, currency]
      requiredResponseFields: [id, status]
`
	svc, contracts, err := Parse("orders-v2.yaml", []byte(doc))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(svc.APIProvides()) != 1 || svc.APIProvides()[0].ContractName != "orders-api" {
		t.Fatalf("expected APIProvides to include orders-api, got %+v", svc.APIProvides())
	}
	if len(svc.APIConsumes()) != 1 {
		t.Fatalf("expected one APIConsumes entry, got %+v", svc.APIConsumes())
	}
	consumption := svc.APIConsumes()[0]
	if consumption.ContractName != "payments-api" || consumption.Operation != "POST /charges" {
		t.Fatalf("unexpected consumption: %+v", consumption)
	}
	if len(contracts) != 1 {
		t.Fatalf("expected one parsed api contract, got %+v", contracts)
	}
	c := contracts[0]
	if c.Key() != (ir.APIContractKey{Name: "orders-api", ProviderService: "orders", ProviderVersion: "v2"}) {
		t.Fatalf("unexpected contract key: %+v", c.Key())
	}
	ep, ok := c.Endpoint("GET /orders/:id")
	if !ok {
		t.Fatalf("expected the declared endpoint to be present")
	}
	if !ep.ResponseShape.RequiresField("total_amount") {
		t.Fatalf("expected total_amount to be a required response field")
	}
}

func TestLoadDir_CollectsAPIContracts(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "orders-v2.yaml", `
apiVersion: rolloutproof.dev/v1beta1
kind: ServiceContract
service: orders
version: v2
api:
  provides:
    - contract: orders-api
      endpoints:
        - operation: "GET /orders/:id"
`)
	reg, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	contracts := reg.APIContracts()
	key := ir.APIContractKey{Name: "orders-api", ProviderService: "orders", ProviderVersion: "v2"}
	if _, ok := contracts[key]; !ok {
		t.Fatalf("expected orders-api@orders@v2 to be collected, got %+v", contracts)
	}
}

// A duplicate APIContractKey can only arise within a single file: two
// files sharing the same provider (service, version) are already
// rejected by the Service-level duplicate check before the api.provides
// loop ever runs. So this exercises the one reachable path — the same
// contract name declared twice in one file's own provides: list.
func TestLoadDir_DuplicateAPIContractWithinOneFileIsError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.yaml", `
apiVersion: rolloutproof.dev/v1beta1
kind: ServiceContract
service: orders
version: v2
api:
  provides:
    - contract: orders-api
      endpoints:
        - operation: "GET /orders/:id"
    - contract: orders-api
      endpoints:
        - operation: "DELETE /orders/:id"
`)
	if _, err := LoadDir(dir); err == nil {
		t.Fatalf("expected an error for the same contract name declared twice by one service version")
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write test fixture: %v", err)
	}
}
