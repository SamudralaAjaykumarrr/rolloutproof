package rolloutplan

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
)

// SchemaAPIVersion is the only apiVersion V1 accepts for a schema
// snapshot file.
const SchemaAPIVersion = "rolloutproof.dev/v1alpha1"

// SchemaKind is the required kind for a schema snapshot file.
const SchemaKind = "Schema"

// ParseError reports a malformed rolloutplan/schema input file.
type ParseError struct {
	File    string
	Message string
}

func (e *ParseError) Error() string { return fmt.Sprintf("rolloutplan: %s: %s", e.File, e.Message) }

type schemaDoc struct {
	APIVersion string       `yaml:"apiVersion"`
	Kind       string       `yaml:"kind"`
	Tables     []tableEntry `yaml:"tables"`
}

type tableEntry struct {
	Name    string        `yaml:"name"`
	Columns []columnEntry `yaml:"columns"`
}

type columnEntry struct {
	Name     string  `yaml:"name"`
	Type     string  `yaml:"type"`
	Nullable bool    `yaml:"nullable"`
	Default  *string `yaml:"default"`
}

// ParseSchema parses a RolloutProof schema-snapshot file — the explicit,
// authored declaration of the database schema a rollout plan's
// migrations apply against (docs/vision.md §5: V1 is a local, offline
// verifier with no live database connection, so the "current schema" is
// an input fact, not something RolloutProof queries). Nullable defaults
// to false (NOT NULL) when omitted, matching the file format's own
// documented convention, not a Postgres-wide default.
func ParseSchema(filename string, src []byte) (ir.Schema, error) {
	dec := yaml.NewDecoder(bytes.NewReader(src))
	dec.KnownFields(true)

	var doc schemaDoc
	if err := dec.Decode(&doc); err != nil {
		return ir.Schema{}, &ParseError{File: filename, Message: err.Error()}
	}
	if doc.APIVersion != SchemaAPIVersion {
		return ir.Schema{}, &ParseError{File: filename, Message: fmt.Sprintf(
			"unsupported apiVersion %q (V1 supports only %q)", doc.APIVersion, SchemaAPIVersion)}
	}
	if doc.Kind != SchemaKind {
		return ir.Schema{}, &ParseError{File: filename, Message: fmt.Sprintf(
			"unsupported kind %q (expected %q)", doc.Kind, SchemaKind)}
	}

	tables := make([]ir.Table, 0, len(doc.Tables))
	for _, te := range doc.Tables {
		if strings.TrimSpace(te.Name) == "" {
			return ir.Schema{}, &ParseError{File: filename, Message: "table with empty name"}
		}
		cols := make([]ir.Column, 0, len(te.Columns))
		for _, ce := range te.Columns {
			if strings.TrimSpace(ce.Name) == "" {
				return ir.Schema{}, &ParseError{File: filename, Message: fmt.Sprintf("table %q: column with empty name", te.Name)}
			}
			if strings.TrimSpace(ce.Type) == "" {
				return ir.Schema{}, &ParseError{File: filename, Message: fmt.Sprintf("table %q: column %q: type must not be empty", te.Name, ce.Name)}
			}
			cols = append(cols, ir.Column{Name: ce.Name, Type: ce.Type, Nullable: ce.Nullable, Default: ce.Default})
		}
		tab, err := ir.NewTable(te.Name, cols)
		if err != nil {
			return ir.Schema{}, &ParseError{File: filename, Message: err.Error()}
		}
		tables = append(tables, tab)
	}

	schema, err := ir.NewSchema(tables)
	if err != nil {
		return ir.Schema{}, &ParseError{File: filename, Message: err.Error()}
	}
	return schema, nil
}
