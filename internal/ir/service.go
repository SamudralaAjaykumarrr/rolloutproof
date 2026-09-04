package ir

import (
	"fmt"
	"sort"
)

// ColumnRef identifies a single column in a single table.
type ColumnRef struct {
	Table  string
	Column string
}

func (c ColumnRef) String() string {
	return c.Table + "." + c.Column
}

func (c ColumnRef) isZero() bool {
	return c.Table == "" && c.Column == ""
}

// compareColumnRefs gives ColumnRef a deterministic total order, used
// wherever a []ColumnRef must be rendered or compared without depending
// on construction order.
func compareColumnRefs(a, b ColumnRef) int {
	if a.Table != b.Table {
		if a.Table < b.Table {
			return -1
		}
		return 1
	}
	if a.Column != b.Column {
		if a.Column < b.Column {
			return -1
		}
		return 1
	}
	return 0
}

// ServiceDependency declares that a service requires another named
// service to be at least at MinCompatibleVersion. An empty
// MinCompatibleVersion means no version constraint was declared — the
// dependency exists, but its ordering-safety cannot be evaluated (see
// RP-ORDER invariants), which is a documented UNKNOWN case, not an
// assumed-compatible one.
type ServiceDependency struct {
	ServiceName          string
	MinCompatibleVersion string
}

func compareServiceDependencies(a, b ServiceDependency) int {
	if a.ServiceName != b.ServiceName {
		if a.ServiceName < b.ServiceName {
			return -1
		}
		return 1
	}
	if a.MinCompatibleVersion != b.MinCompatibleVersion {
		if a.MinCompatibleVersion < b.MinCompatibleVersion {
			return -1
		}
		return 1
	}
	return 0
}

// Service is a version-scoped fact sheet: what one named service, at one
// declared version, reads, writes, and depends on. Two versions of the
// same service are two distinct Service values sharing a Name.
//
// A Service is never constructed with inferred facts (docs/adr/0004): its
// SchemaReads/SchemaWrites/DependsOn are exactly what a contract-metadata
// file declared, including "declared nothing" as a distinct, valid,
// closed-world fact from "no file exists for this service version at
// all" (the latter is a loader-level EvidenceGap, not an empty Service).
type Service struct {
	Name    string
	Version string

	// SourceFile is the contract metadata file this Service was parsed
	// from, if any (empty when constructed directly, e.g. in tests). It
	// exists purely for Evidence citation (docs/architecture.md §11) —
	// no invariant may branch on its value.
	SourceFile string

	schemaReads  []ColumnRef
	schemaWrites []ColumnRef
	dependsOn    []ServiceDependency
}

// Key uniquely identifies a Service by name and version, suitable for use
// as a map key when building the lookup table invariants query.
type ServiceKey struct {
	Name    string
	Version string
}

func (s Service) Key() ServiceKey {
	return ServiceKey{Name: s.Name, Version: s.Version}
}

func (s Service) String() string {
	return fmt.Sprintf("%s@%s", s.Name, s.Version)
}

// SchemaReads returns the columns this service version declares it reads,
// in a fixed deterministic order independent of construction order.
func (s Service) SchemaReads() []ColumnRef { return append([]ColumnRef(nil), s.schemaReads...) }

// SchemaWrites returns the columns this service version declares it
// writes, in a fixed deterministic order.
func (s Service) SchemaWrites() []ColumnRef { return append([]ColumnRef(nil), s.schemaWrites...) }

// DependsOn returns the declared service dependencies, in a fixed
// deterministic order.
func (s Service) DependsOn() []ServiceDependency {
	return append([]ServiceDependency(nil), s.dependsOn...)
}

// ReadsColumn reports whether this service version declares it reads the
// given column.
func (s Service) ReadsColumn(c ColumnRef) bool {
	return containsColumnRef(s.schemaReads, c)
}

// WritesColumn reports whether this service version declares it writes
// the given column.
func (s Service) WritesColumn(c ColumnRef) bool {
	return containsColumnRef(s.schemaWrites, c)
}

// TouchesTable reports whether this service version declares any read or
// write on the given table at all. Used to distinguish "declared writes
// on this table, but not this column" (a real absence, RP-DB-004) from
// "never mentions this table" (an evidence gap).
func (s Service) TouchesTable(table string) bool {
	for _, c := range s.schemaReads {
		if c.Table == table {
			return true
		}
	}
	for _, c := range s.schemaWrites {
		if c.Table == table {
			return true
		}
	}
	return false
}

func containsColumnRef(list []ColumnRef, c ColumnRef) bool {
	for _, item := range list {
		if item == c {
			return true
		}
	}
	return false
}

// NewService validates and constructs a Service. Duplicate ColumnRef or
// ServiceDependency entries are deduplicated, not rejected (docs/adr/0007
// duplicate-handling rule) — the same fact declared twice is still just
// one fact.
func NewService(name, version string, reads, writes []ColumnRef, dependsOn []ServiceDependency) (Service, error) {
	if name == "" {
		return Service{}, fmt.Errorf("ir: service name must not be empty")
	}
	if version == "" {
		return Service{}, fmt.Errorf("ir: service %q: version must not be empty", name)
	}
	for _, c := range reads {
		if c.isZero() || c.Table == "" || c.Column == "" {
			return Service{}, fmt.Errorf("ir: service %s@%s: invalid schema read entry %+v", name, version, c)
		}
	}
	for _, c := range writes {
		if c.isZero() || c.Table == "" || c.Column == "" {
			return Service{}, fmt.Errorf("ir: service %s@%s: invalid schema write entry %+v", name, version, c)
		}
	}
	for _, d := range dependsOn {
		if d.ServiceName == "" {
			return Service{}, fmt.Errorf("ir: service %s@%s: dependency with empty service name", name, version)
		}
	}

	s := Service{
		Name:         name,
		Version:      version,
		schemaReads:  dedupColumnRefs(reads),
		schemaWrites: dedupColumnRefs(writes),
		dependsOn:    dedupServiceDependencies(dependsOn),
	}
	return s, nil
}

func dedupColumnRefs(in []ColumnRef) []ColumnRef {
	seen := make(map[ColumnRef]struct{}, len(in))
	out := make([]ColumnRef, 0, len(in))
	for _, c := range in {
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return compareColumnRefs(out[i], out[j]) < 0 })
	return out
}

func dedupServiceDependencies(in []ServiceDependency) []ServiceDependency {
	seen := make(map[ServiceDependency]struct{}, len(in))
	out := make([]ServiceDependency, 0, len(in))
	for _, d := range in {
		if _, ok := seen[d]; ok {
			continue
		}
		seen[d] = struct{}{}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return compareServiceDependencies(out[i], out[j]) < 0 })
	return out
}
