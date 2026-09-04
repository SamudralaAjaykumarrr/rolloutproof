package ir

import (
	"fmt"
	"sort"
)

// Field is one named field in a request or response Shape, with whether
// the provider guarantees it (docs/architecture.md §2.5). V1 models
// presence and requiredness only — deep type compatibility is a named,
// out-of-scope limitation (docs/invariants.md RP-API-002/003).
type Field struct {
	Name     string
	Required bool
}

// Shape is the field set of one side (request or response) of one
// Endpoint, as the provider actually declares it.
type Shape struct {
	fields map[string]Field
}

// NewShape validates and constructs a Shape. A duplicate field name is a
// construction error — two conflicting Required values for the same
// field name is a nonsensical contract, not a fact to silently resolve.
func NewShape(fields []Field) (Shape, error) {
	m := make(map[string]Field, len(fields))
	for _, f := range fields {
		if f.Name == "" {
			return Shape{}, fmt.Errorf("ir: shape: field with empty name")
		}
		if existing, dup := m[f.Name]; dup && existing.Required != f.Required {
			return Shape{}, fmt.Errorf("ir: shape: field %q declared twice with different Required values", f.Name)
		}
		m[f.Name] = f
	}
	return Shape{fields: m}, nil
}

// Fields returns this shape's fields sorted by name.
func (s Shape) Fields() []Field {
	out := make([]Field, 0, len(s.fields))
	for _, f := range s.fields {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// HasField reports whether name is present in this shape at all
// (regardless of Required) — the fact RP-API-003 needs: a field
// disappearing from a response, not merely losing its Required flag.
func (s Shape) HasField(name string) bool {
	_, ok := s.fields[name]
	return ok
}

// RequiresField reports whether name is present and marked Required —
// the fact RP-API-002 needs: the provider will reject a request that
// omits it.
func (s Shape) RequiresField(name string) bool {
	f, ok := s.fields[name]
	return ok && f.Required
}

// Endpoint is one operation an APIContract exposes, with the shape of
// its request and response as the provider version declares them.
type Endpoint struct {
	Operation     string // e.g. "GET /orders/:id"
	RequestShape  Shape
	ResponseShape Shape
}

// APIContractKey identifies one provider version's declaration of one
// named contract — the unit RP-API invariants look up per live provider
// version in a reachable state.
type APIContractKey struct {
	Name            string
	ProviderService string
	ProviderVersion string
}

// APIContract is a version-scoped fact sheet, the API-surface analog of
// Service's schema facts (docs/architecture.md §2.5): "service X at
// version V provides contract C with these endpoints." Two versions of
// the same service that provide the same contract are two distinct
// APIContract values sharing Name and ProviderService.
type APIContract struct {
	Name            string
	ProviderService string
	ProviderVersion string

	// SourceFile is the contract metadata file this was parsed from, for
	// Evidence citation (docs/architecture.md §11) — no invariant may
	// branch on its value.
	SourceFile string

	endpoints map[string]Endpoint
}

// Key returns this contract's lookup key.
func (c APIContract) Key() APIContractKey {
	return APIContractKey{Name: c.Name, ProviderService: c.ProviderService, ProviderVersion: c.ProviderVersion}
}

// Endpoint looks up one operation by its exact string.
func (c APIContract) Endpoint(operation string) (Endpoint, bool) {
	e, ok := c.endpoints[operation]
	return e, ok
}

// Endpoints returns every declared operation, sorted for determinism.
func (c APIContract) Endpoints() []Endpoint {
	out := make([]Endpoint, 0, len(c.endpoints))
	for _, e := range c.endpoints {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Operation < out[j].Operation })
	return out
}

// NewAPIContract validates and constructs an APIContract.
func NewAPIContract(name, providerService, providerVersion string, endpoints []Endpoint) (APIContract, error) {
	if name == "" {
		return APIContract{}, fmt.Errorf("ir: api contract: name must not be empty")
	}
	if providerService == "" {
		return APIContract{}, fmt.Errorf("ir: api contract %q: providerService must not be empty", name)
	}
	if providerVersion == "" {
		return APIContract{}, fmt.Errorf("ir: api contract %q: providerVersion must not be empty", name)
	}
	m := make(map[string]Endpoint, len(endpoints))
	for _, e := range endpoints {
		if e.Operation == "" {
			return APIContract{}, fmt.Errorf("ir: api contract %q: endpoint with empty operation", name)
		}
		if _, dup := m[e.Operation]; dup {
			return APIContract{}, fmt.Errorf("ir: api contract %q: duplicate operation %q", name, e.Operation)
		}
		m[e.Operation] = e
	}
	return APIContract{Name: name, ProviderService: providerService, ProviderVersion: providerVersion, endpoints: m}, nil
}

// APIProvision references a contract this service version provides — the
// actual Endpoints live on the matching APIContract, looked up by
// APIContractKey{ContractName, this Service's Name, this Service's
// Version} in the registry internal/parser/contract builds.
type APIProvision struct {
	ContractName string
}

// APIConsumption is one declared expectation a service version has of one
// operation on a named contract: which request fields it sends, and
// which response fields it requires. This is the consumer's own
// declared facts, not a reference to the provider's historical shape at
// some version — docs/architecture.md §8 leaves the exact contract
// metadata schema to implementation, and this shape is what
// docs/adr/0007's forward-compatibility policy anticipates as
// "api.consumes" (see the ADR and internal/parser/contract's v1beta1
// support).
type APIConsumption struct {
	ContractName           string
	Operation              string
	RequestFieldsSent      []string
	RequiredResponseFields []string
}

func (c APIConsumption) validate() error {
	if c.ContractName == "" {
		return fmt.Errorf("ir: api consumption: contract name must not be empty")
	}
	if c.Operation == "" {
		return fmt.Errorf("ir: api consumption %q: operation must not be empty", c.ContractName)
	}
	return nil
}
