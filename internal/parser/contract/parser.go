// Package contract parses RolloutProof's service/schema contract metadata
// format (docs/adr/0007) into ir.Service values. This is the one format
// RolloutProof defines itself, because inferring schema access from
// application source code is explicitly out of scope for V1
// (docs/adr/0004).
//
// Parsing is strict: any field not named in docs/adr/0007's schema is a
// parse error, never silently ignored. A typo'd field (e.g. "shema:"
// instead of "schema:") must be visible as a build/CI failure, not as a
// silently-empty SchemaReads/Writes that could masquerade as the
// legitimate closed-world "this version declares no reads" fact.
package contract

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
)

// SupportedAPIVersion is the original apiVersion V1 accepts
// (docs/adr/0007): schema.reads/writes and dependencies only, no api
// block.
const SupportedAPIVersion = "rolloutproof.dev/v1alpha1"

// SupportedAPIVersionBeta additionally accepts the api.provides/api.consumes
// block RP-API needs. docs/adr/0007's own forward-compatibility policy
// anticipates exactly this: "a future version that needs new fields (e.g.
// api.provides/api.consumes for RP-API invariants)... ships as
// rolloutproof.dev/v1beta1... never by silently ignoring fields it
// doesn't recognize under the current one" — so a v1alpha1 document that
// includes an api: block is a parse error, not a tolerated extension; see
// the api-block-under-v1alpha1 check in Parse.
const SupportedAPIVersionBeta = "rolloutproof.dev/v1beta1"

// RequiredKind is the only kind V1 accepts.
const RequiredKind = "ServiceContract"

// ParseError reports a malformed or unsupported contract metadata file.
type ParseError struct {
	File    string
	Message string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("contract: %s: %s", e.File, e.Message)
}

type rawDocument struct {
	APIVersion   string            `yaml:"apiVersion"`
	Kind         string            `yaml:"kind"`
	Service      string            `yaml:"service"`
	Version      string            `yaml:"version"`
	Schema       *schemaBlock      `yaml:"schema"`
	Dependencies []dependencyEntry `yaml:"dependencies"`
	API          *apiBlock         `yaml:"api"`
}

type schemaBlock struct {
	Reads  []string `yaml:"reads"`
	Writes []string `yaml:"writes"`
}

// apiBlock is SupportedAPIVersionBeta's addition (docs/adr/0007's
// anticipated api.provides/api.consumes) — RolloutProof's own format for
// RP-API's required evidence (docs/architecture.md §2.5), not a
// reference into a provider's historical shape at some version: a
// consumer declares exactly what it sends and requires, directly.
type apiBlock struct {
	Provides []provideEntry `yaml:"provides"`
	Consumes []consumeEntry `yaml:"consumes"`
}

type provideEntry struct {
	Contract  string          `yaml:"contract"`
	Endpoints []endpointEntry `yaml:"endpoints"`
}

type endpointEntry struct {
	Operation string       `yaml:"operation"`
	Request   *fieldsBlock `yaml:"request"`
	Response  *fieldsBlock `yaml:"response"`
}

type fieldsBlock struct {
	Fields []fieldEntry `yaml:"fields"`
}

type fieldEntry struct {
	Name     string `yaml:"name"`
	Required bool   `yaml:"required"`
}

type consumeEntry struct {
	Contract               string   `yaml:"contract"`
	Operation              string   `yaml:"operation"`
	RequestFieldsSent      []string `yaml:"requestFieldsSent"`
	RequiredResponseFields []string `yaml:"requiredResponseFields"`
}

// dependencyEntry accepts either a bare service-name string or a mapping
// {service, minCompatibleVersion} (docs/adr/0007). It implements its own
// strict validation because yaml.v3's Decoder-level KnownFields checking
// does not extend into a type with a custom UnmarshalYAML method.
type dependencyEntry struct {
	Service              string
	MinCompatibleVersion string
}

func (d *dependencyEntry) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		var s string
		if err := node.Decode(&s); err != nil {
			return err
		}
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("dependency entry must not be empty")
		}
		d.Service = s
		return nil

	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i].Value
			val := node.Content[i+1]
			switch key {
			case "service":
				if err := val.Decode(&d.Service); err != nil {
					return fmt.Errorf("dependency.service: %w", err)
				}
			case "minCompatibleVersion":
				if err := val.Decode(&d.MinCompatibleVersion); err != nil {
					return fmt.Errorf("dependency.minCompatibleVersion: %w", err)
				}
			default:
				return fmt.Errorf("unknown field %q in dependency entry", key)
			}
		}
		if strings.TrimSpace(d.Service) == "" {
			return fmt.Errorf("dependency entry mapping must set 'service'")
		}
		return nil

	default:
		return fmt.Errorf("dependency entry must be a string or a mapping, got %v", node.Kind)
	}
}

// Parse parses one contract metadata file's contents into an ir.Service
// and, when the file provides any (SupportedAPIVersionBeta only), the
// ir.APIContract values it declares. filename is used only for error
// messages.
func Parse(filename string, src []byte) (ir.Service, []ir.APIContract, error) {
	dec := yaml.NewDecoder(bytes.NewReader(src))
	dec.KnownFields(true)

	var doc rawDocument
	if err := dec.Decode(&doc); err != nil {
		return ir.Service{}, nil, &ParseError{File: filename, Message: err.Error()}
	}

	if doc.APIVersion != SupportedAPIVersion && doc.APIVersion != SupportedAPIVersionBeta {
		return ir.Service{}, nil, &ParseError{File: filename, Message: fmt.Sprintf(
			"unsupported apiVersion %q (V1 supports %q and %q)", doc.APIVersion, SupportedAPIVersion, SupportedAPIVersionBeta)}
	}
	if doc.API != nil && doc.APIVersion != SupportedAPIVersionBeta {
		return ir.Service{}, nil, &ParseError{File: filename, Message: fmt.Sprintf(
			"the \"api\" block requires apiVersion %q (docs/adr/0007)", SupportedAPIVersionBeta)}
	}
	if doc.Kind != RequiredKind {
		return ir.Service{}, nil, &ParseError{File: filename, Message: fmt.Sprintf(
			"unsupported kind %q (expected %q)", doc.Kind, RequiredKind)}
	}
	if strings.TrimSpace(doc.Service) == "" {
		return ir.Service{}, nil, &ParseError{File: filename, Message: "service must not be empty"}
	}
	if strings.TrimSpace(doc.Version) == "" {
		return ir.Service{}, nil, &ParseError{File: filename, Message: "version must not be empty"}
	}

	var reads, writes []ir.ColumnRef
	if doc.Schema != nil {
		var err error
		reads, err = parseColumnRefs(filename, "schema.reads", doc.Schema.Reads)
		if err != nil {
			return ir.Service{}, nil, err
		}
		writes, err = parseColumnRefs(filename, "schema.writes", doc.Schema.Writes)
		if err != nil {
			return ir.Service{}, nil, err
		}
	}

	deps := make([]ir.ServiceDependency, 0, len(doc.Dependencies))
	for _, d := range doc.Dependencies {
		deps = append(deps, ir.ServiceDependency{ServiceName: d.Service, MinCompatibleVersion: d.MinCompatibleVersion})
	}

	var provides []ir.APIProvision
	var consumes []ir.APIConsumption
	var contracts []ir.APIContract
	if doc.API != nil {
		var err error
		provides, contracts, err = parseProvides(filename, doc.Service, doc.Version, doc.API.Provides)
		if err != nil {
			return ir.Service{}, nil, err
		}
		consumes, err = parseConsumes(filename, doc.API.Consumes)
		if err != nil {
			return ir.Service{}, nil, err
		}
	}

	svc, err := ir.NewServiceWithAPI(doc.Service, doc.Version, reads, writes, deps, provides, consumes)
	if err != nil {
		return ir.Service{}, nil, &ParseError{File: filename, Message: err.Error()}
	}
	svc.SourceFile = filename
	return svc, contracts, nil
}

// parseProvides builds this service version's APIProvision references and
// the full ir.APIContract values (with their Endpoints) its provides
// entries declare.
func parseProvides(filename, service, version string, entries []provideEntry) ([]ir.APIProvision, []ir.APIContract, error) {
	provides := make([]ir.APIProvision, 0, len(entries))
	contracts := make([]ir.APIContract, 0, len(entries))
	for _, pe := range entries {
		if strings.TrimSpace(pe.Contract) == "" {
			return nil, nil, &ParseError{File: filename, Message: "api.provides entry: \"contract\" must not be empty"}
		}
		endpoints := make([]ir.Endpoint, 0, len(pe.Endpoints))
		for _, ee := range pe.Endpoints {
			if strings.TrimSpace(ee.Operation) == "" {
				return nil, nil, &ParseError{File: filename, Message: fmt.Sprintf(
					"api.provides[%s]: endpoint entry with empty \"operation\"", pe.Contract)}
			}
			reqShape, err := parseFieldsBlock(filename, pe.Contract, ee.Operation, "request", ee.Request)
			if err != nil {
				return nil, nil, err
			}
			respShape, err := parseFieldsBlock(filename, pe.Contract, ee.Operation, "response", ee.Response)
			if err != nil {
				return nil, nil, err
			}
			endpoints = append(endpoints, ir.Endpoint{Operation: ee.Operation, RequestShape: reqShape, ResponseShape: respShape})
		}
		contract, err := ir.NewAPIContract(pe.Contract, service, version, endpoints)
		if err != nil {
			return nil, nil, &ParseError{File: filename, Message: err.Error()}
		}
		contract.SourceFile = filename
		contracts = append(contracts, contract)
		provides = append(provides, ir.APIProvision{ContractName: pe.Contract})
	}
	return provides, contracts, nil
}

func parseFieldsBlock(filename, contract, operation, side string, block *fieldsBlock) (ir.Shape, error) {
	if block == nil {
		return ir.NewShape(nil)
	}
	fields := make([]ir.Field, 0, len(block.Fields))
	for _, fe := range block.Fields {
		if strings.TrimSpace(fe.Name) == "" {
			return ir.Shape{}, &ParseError{File: filename, Message: fmt.Sprintf(
				"api.provides[%s] %s %s: field with empty name", contract, operation, side)}
		}
		fields = append(fields, ir.Field{Name: fe.Name, Required: fe.Required})
	}
	shape, err := ir.NewShape(fields)
	if err != nil {
		return ir.Shape{}, &ParseError{File: filename, Message: err.Error()}
	}
	return shape, nil
}

func parseConsumes(filename string, entries []consumeEntry) ([]ir.APIConsumption, error) {
	out := make([]ir.APIConsumption, 0, len(entries))
	for _, ce := range entries {
		if strings.TrimSpace(ce.Contract) == "" {
			return nil, &ParseError{File: filename, Message: "api.consumes entry: \"contract\" must not be empty"}
		}
		if strings.TrimSpace(ce.Operation) == "" {
			return nil, &ParseError{File: filename, Message: fmt.Sprintf(
				"api.consumes[%s]: \"operation\" must not be empty", ce.Contract)}
		}
		out = append(out, ir.APIConsumption{
			ContractName:           ce.Contract,
			Operation:              ce.Operation,
			RequestFieldsSent:      append([]string(nil), ce.RequestFieldsSent...),
			RequiredResponseFields: append([]string(nil), ce.RequiredResponseFields...),
		})
	}
	return out, nil
}

// parseColumnRefs splits each "table.column" entry, per docs/adr/0007: a
// value with zero or more than one '.' is a validation error, not a
// best-effort guess.
func parseColumnRefs(filename, field string, entries []string) ([]ir.ColumnRef, error) {
	out := make([]ir.ColumnRef, 0, len(entries))
	for _, e := range entries {
		if strings.Count(e, ".") != 1 {
			return nil, &ParseError{File: filename, Message: fmt.Sprintf(
				"%s: invalid column reference %q (expected exactly one \".\")", field, e)}
		}
		idx := strings.Index(e, ".")
		table, column := e[:idx], e[idx+1:]
		if table == "" || column == "" {
			return nil, &ParseError{File: filename, Message: fmt.Sprintf(
				"%s: invalid column reference %q", field, e)}
		}
		out = append(out, ir.ColumnRef{Table: table, Column: column})
	}
	return out, nil
}

// Registry is a lookup table of parsed contracts, keyed by (service,
// version) for Service facts and by APIContractKey for API contracts. It
// is the input internal/invariant uses to look up what a live version
// reads/writes/depends on/provides — a lookup miss is the concrete
// EvidenceGap event (docs/architecture.md §6), not this package's
// concern.
type Registry struct {
	services     map[ir.ServiceKey]ir.Service
	apiContracts map[ir.APIContractKey]ir.APIContract
}

// Lookup returns the parsed Service for (name, version), if a contract
// file declared one.
func (r *Registry) Lookup(name, version string) (ir.Service, bool) {
	if r == nil {
		return ir.Service{}, false
	}
	s, ok := r.services[ir.ServiceKey{Name: name, Version: version}]
	return s, ok
}

// Services returns a defensive copy of every parsed contract, keyed by
// (service, version). internal/invariant depends only on this plain map
// type, not on this package, keeping the parser/invariant dependency
// direction in docs/architecture.md §10 intact.
func (r *Registry) Services() map[ir.ServiceKey]ir.Service {
	out := make(map[ir.ServiceKey]ir.Service, len(r.services))
	for k, v := range r.services {
		out[k] = v
	}
	return out
}

// APIContracts returns a defensive copy of every parsed api.provides
// entry, keyed by APIContractKey.
func (r *Registry) APIContracts() map[ir.APIContractKey]ir.APIContract {
	out := make(map[ir.APIContractKey]ir.APIContract, len(r.apiContracts))
	for k, v := range r.apiContracts {
		out[k] = v
	}
	return out
}

// LoadDir parses every *.yaml/*.yml file directly inside dir (no
// recursion — docs/adr/0007 does not define nested-directory discovery)
// as a contract metadata file. Two files declaring the same
// (service, version) pair, or the same API contract key, is a load
// error: it is real ambiguity, not something to resolve by silently
// preferring one file over the other.
func LoadDir(dir string) (*Registry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("contract: reading directory %s: %w", dir, err)
	}

	reg := &Registry{services: map[ir.ServiceKey]ir.Service{}, apiContracts: map[ir.APIContractKey]ir.APIContract{}}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("contract: reading %s: %w", path, err)
		}
		svc, contracts, err := Parse(path, data)
		if err != nil {
			return nil, err
		}
		key := svc.Key()
		if existing, dup := reg.services[key]; dup {
			return nil, &ParseError{File: path, Message: fmt.Sprintf(
				"duplicate contract for %s@%s (already declared in a prior file)", existing.Name, existing.Version)}
		}
		reg.services[key] = svc
		for _, c := range contracts {
			ckey := c.Key()
			if _, dup := reg.apiContracts[ckey]; dup {
				return nil, &ParseError{File: path, Message: fmt.Sprintf(
					"duplicate api contract %q for %s@%s (already declared in a prior file)", c.Name, c.ProviderService, c.ProviderVersion)}
			}
			reg.apiContracts[ckey] = c
		}
	}
	return reg, nil
}
