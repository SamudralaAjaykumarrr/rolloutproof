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

// SupportedAPIVersion is the only apiVersion V1 accepts (docs/adr/0007).
const SupportedAPIVersion = "rolloutproof.dev/v1alpha1"

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
}

type schemaBlock struct {
	Reads  []string `yaml:"reads"`
	Writes []string `yaml:"writes"`
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

// Parse parses one contract metadata file's contents into an ir.Service.
// filename is used only for error messages.
func Parse(filename string, src []byte) (ir.Service, error) {
	dec := yaml.NewDecoder(bytes.NewReader(src))
	dec.KnownFields(true)

	var doc rawDocument
	if err := dec.Decode(&doc); err != nil {
		return ir.Service{}, &ParseError{File: filename, Message: err.Error()}
	}

	if doc.APIVersion != SupportedAPIVersion {
		return ir.Service{}, &ParseError{File: filename, Message: fmt.Sprintf(
			"unsupported apiVersion %q (V1 supports only %q)", doc.APIVersion, SupportedAPIVersion)}
	}
	if doc.Kind != RequiredKind {
		return ir.Service{}, &ParseError{File: filename, Message: fmt.Sprintf(
			"unsupported kind %q (expected %q)", doc.Kind, RequiredKind)}
	}
	if strings.TrimSpace(doc.Service) == "" {
		return ir.Service{}, &ParseError{File: filename, Message: "service must not be empty"}
	}
	if strings.TrimSpace(doc.Version) == "" {
		return ir.Service{}, &ParseError{File: filename, Message: "version must not be empty"}
	}

	var reads, writes []ir.ColumnRef
	if doc.Schema != nil {
		var err error
		reads, err = parseColumnRefs(filename, "schema.reads", doc.Schema.Reads)
		if err != nil {
			return ir.Service{}, err
		}
		writes, err = parseColumnRefs(filename, "schema.writes", doc.Schema.Writes)
		if err != nil {
			return ir.Service{}, err
		}
	}

	deps := make([]ir.ServiceDependency, 0, len(doc.Dependencies))
	for _, d := range doc.Dependencies {
		deps = append(deps, ir.ServiceDependency{ServiceName: d.Service, MinCompatibleVersion: d.MinCompatibleVersion})
	}

	svc, err := ir.NewService(doc.Service, doc.Version, reads, writes, deps)
	if err != nil {
		return ir.Service{}, &ParseError{File: filename, Message: err.Error()}
	}
	svc.SourceFile = filename
	return svc, nil
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
// version). It is the input internal/invariant uses to look up what a
// live version reads/writes/depends on — a lookup miss is the concrete
// EvidenceGap event (docs/architecture.md §6), not this package's
// concern.
type Registry struct {
	services map[ir.ServiceKey]ir.Service
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

// LoadDir parses every *.yaml/*.yml file directly inside dir (no
// recursion — docs/adr/0007 does not define nested-directory discovery)
// as a contract metadata file. Two files declaring the same
// (service, version) pair is a load error: it is real ambiguity, not
// something to resolve by silently preferring one file over the other.
func LoadDir(dir string) (*Registry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("contract: reading directory %s: %w", dir, err)
	}

	reg := &Registry{services: map[ir.ServiceKey]ir.Service{}}
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
		svc, err := Parse(path, data)
		if err != nil {
			return nil, err
		}
		key := svc.Key()
		if existing, dup := reg.services[key]; dup {
			return nil, &ParseError{File: path, Message: fmt.Sprintf(
				"duplicate contract for %s@%s (already declared in a prior file)", existing.Name, existing.Version)}
		}
		reg.services[key] = svc
	}
	return reg, nil
}
