package ir

import (
	"reflect"
	"testing"
)

func TestNewService_ValidConstruction(t *testing.T) {
	s, err := NewService("api", "v1",
		[]ColumnRef{{Table: "users", Column: "email"}},
		[]ColumnRef{{Table: "users", Column: "name"}},
		[]ServiceDependency{{ServiceName: "payments", MinCompatibleVersion: "v3"}},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Key() != (ServiceKey{Name: "api", Version: "v1"}) {
		t.Fatalf("unexpected key: %+v", s.Key())
	}
	if !s.ReadsColumn(ColumnRef{Table: "users", Column: "email"}) {
		t.Fatalf("expected ReadsColumn to be true")
	}
	if s.ReadsColumn(ColumnRef{Table: "users", Column: "name"}) {
		t.Fatalf("expected ReadsColumn to be false for a write-only column")
	}
	if !s.WritesColumn(ColumnRef{Table: "users", Column: "name"}) {
		t.Fatalf("expected WritesColumn to be true")
	}
}

func TestNewService_RejectsEmptyName(t *testing.T) {
	if _, err := NewService("", "v1", nil, nil, nil); err == nil {
		t.Fatalf("expected error for empty service name")
	}
}

func TestNewService_RejectsEmptyVersion(t *testing.T) {
	if _, err := NewService("api", "", nil, nil, nil); err == nil {
		t.Fatalf("expected error for empty version")
	}
}

func TestNewService_RejectsInvalidColumnRef(t *testing.T) {
	if _, err := NewService("api", "v1", []ColumnRef{{Table: "users"}}, nil, nil); err == nil {
		t.Fatalf("expected error for column ref with empty column")
	}
	if _, err := NewService("api", "v1", nil, []ColumnRef{{Column: "email"}}, nil); err == nil {
		t.Fatalf("expected error for column ref with empty table")
	}
}

func TestNewService_RejectsDependencyWithEmptyServiceName(t *testing.T) {
	if _, err := NewService("api", "v1", nil, nil, []ServiceDependency{{MinCompatibleVersion: "v1"}}); err == nil {
		t.Fatalf("expected error for dependency with empty service name")
	}
}

func TestNewService_DeduplicatesSchemaReads(t *testing.T) {
	s, err := NewService("api", "v1",
		[]ColumnRef{
			{Table: "users", Column: "email"},
			{Table: "users", Column: "email"},
		}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := s.SchemaReads(); len(got) != 1 {
		t.Fatalf("expected deduplication to leave 1 entry, got %d: %+v", len(got), got)
	}
}

func TestNewService_DeduplicatesDependencies(t *testing.T) {
	s, err := NewService("checkout", "v1", nil, nil, []ServiceDependency{
		{ServiceName: "payments", MinCompatibleVersion: "v3"},
		{ServiceName: "payments", MinCompatibleVersion: "v3"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := s.DependsOn(); len(got) != 1 {
		t.Fatalf("expected deduplication to leave 1 entry, got %d: %+v", len(got), got)
	}
}

func TestNewService_DeterministicOrderingIndependentOfInputOrder(t *testing.T) {
	a, err := NewService("api", "v1", []ColumnRef{
		{Table: "users", Column: "z"},
		{Table: "users", Column: "a"},
		{Table: "orders", Column: "id"},
	}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b, err := NewService("api", "v1", []ColumnRef{
		{Table: "orders", Column: "id"},
		{Table: "users", Column: "a"},
		{Table: "users", Column: "z"},
	}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(a.SchemaReads(), b.SchemaReads()) {
		t.Fatalf("expected identical ordering regardless of construction order: %+v vs %+v", a.SchemaReads(), b.SchemaReads())
	}
}

func TestService_TouchesTable(t *testing.T) {
	s, err := NewService("api", "v1", nil, []ColumnRef{{Table: "users", Column: "name"}}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !s.TouchesTable("users") {
		t.Fatalf("expected TouchesTable(users) to be true")
	}
	if s.TouchesTable("orders") {
		t.Fatalf("expected TouchesTable(orders) to be false")
	}
}

func TestService_SchemaReadsReturnsDefensiveCopy(t *testing.T) {
	s, err := NewService("api", "v1", []ColumnRef{{Table: "users", Column: "email"}}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	reads := s.SchemaReads()
	reads[0] = ColumnRef{Table: "mutated", Column: "mutated"}
	if s.SchemaReads()[0] != (ColumnRef{Table: "users", Column: "email"}) {
		t.Fatalf("mutating the returned slice must not affect the Service")
	}
}
