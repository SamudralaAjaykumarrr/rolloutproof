// Package k8s parses Kubernetes Deployment manifests into ir.Workload
// values (docs/architecture.md §2.2, §8). It unmarshals via upstream
// k8s.io/api/apps/v1 types and sigs.k8s.io/yaml, so YAML and IntOrString
// semantics are delegated to Kubernetes' own type definitions rather than
// reimplemented (docs/vision.md §6's instruction not to reinvent
// semantics upstream already defines).
//
// V1 supports exactly one Kind: "Deployment" (docs/vision.md §6). A
// multi-document YAML file may contain other resource kinds; they are
// reported as Skipped, never silently merged into a Workload or treated
// as a parse error on their own.
//
// RolloutProof-specific conventions — Kubernetes has no native field for
// any of these, so V1 reads them from labels/annotations under a
// reserved prefix, explicit and documented rather than guessed:
//
//   - ServiceName: the label "rolloutproof.dev/service" on the
//     Deployment's own metadata, or the Deployment's name if absent.
//   - Version: the label "rolloutproof.dev/version" on the Deployment's
//     own metadata, or the image tag of the pod template's first
//     container if absent (whichever image is found first is used;
//     multi-container pods with independently versioned containers are
//     not modeled in V1).
//   - Workload.Readiness.WaitsOnDependencies: the annotation
//     "rolloutproof.dev/waits-on-dependencies" on the Deployment's own
//     metadata, parsed as a bool; absent or unparseable means false —
//     see docs/invariants.md RP-K8S-002 for why this is a distinct fact
//     from HasReadinessProbe.
//   - Workload.DependsOn: the annotation "rolloutproof.dev/depends-on"
//     on the Deployment's own metadata, a comma-separated list of
//     service names.
package k8s

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	sigsyaml "sigs.k8s.io/yaml"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
)

const (
	labelService      = "rolloutproof.dev/service"
	labelVersion      = "rolloutproof.dev/version"
	annotationWaits   = "rolloutproof.dev/waits-on-dependencies"
	annotationDepends = "rolloutproof.dev/depends-on"
)

// ParseError reports a document that could not be parsed at all —
// invalid YAML, or a Deployment document missing fields Kubernetes
// itself requires (e.g. no container image). It is distinct from a
// Skipped document, which is well-formed but simply not a Deployment.
type ParseError struct {
	Message string
}

func (e *ParseError) Error() string { return "k8s: " + e.Message }

// Skipped records one YAML document in a multi-document stream that was
// not a Deployment — reported, never silently dropped, so a caller can
// tell "this file had no Deployment at all" from "this file's Deployment
// failed to parse."
type Skipped struct {
	Kind string
	Name string
}

type typeMeta struct {
	Kind string `json:"kind"`
}

type partialMeta struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
}

// ParseAll splits src as a multi-document YAML stream (documents
// separated by a "---" line, per YAML/Kubernetes convention) and parses
// every Deployment document into an ir.Workload. Non-Deployment documents
// are returned in skipped, never as an error.
func ParseAll(src []byte) (workloads []ir.Workload, skipped []Skipped, err error) {
	reader := k8syaml.NewYAMLReader(bufio.NewReader(bytes.NewReader(src)))
	for {
		doc, rerr := reader.Read()
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return nil, nil, &ParseError{Message: fmt.Sprintf("failed to split YAML document stream: %v", rerr)}
		}
		if len(bytes.TrimSpace(doc)) == 0 {
			continue
		}

		var tm typeMeta
		if err := sigsyaml.Unmarshal(doc, &tm); err != nil {
			return nil, nil, &ParseError{Message: fmt.Sprintf("invalid YAML document: %v", err)}
		}
		if tm.Kind != "Deployment" {
			var meta partialMeta
			_ = sigsyaml.Unmarshal(doc, &meta)
			skipped = append(skipped, Skipped{Kind: tm.Kind, Name: meta.Metadata.Name})
			continue
		}

		w, perr := ParseDeployment(doc)
		if perr != nil {
			return nil, nil, perr
		}
		workloads = append(workloads, w)
	}
	return workloads, skipped, nil
}

// ParseDeployment parses one YAML document, which must be a Deployment,
// into an ir.Workload.
func ParseDeployment(src []byte) (ir.Workload, error) {
	var d appsv1.Deployment
	if err := sigsyaml.Unmarshal(src, &d); err != nil {
		return ir.Workload{}, &ParseError{Message: fmt.Sprintf("invalid Deployment manifest: %v", err)}
	}
	if d.Kind != "" && d.Kind != "Deployment" {
		return ir.Workload{}, &ParseError{Message: fmt.Sprintf("expected kind Deployment, got %q", d.Kind)}
	}
	if d.Name == "" {
		return ir.Workload{}, &ParseError{Message: "Deployment manifest has no metadata.name"}
	}

	serviceName := d.Labels[labelService]
	if serviceName == "" {
		serviceName = d.Name
	}

	version, verr := extractVersion(d)
	if verr != nil {
		return ir.Workload{}, verr
	}

	replicas := 1
	if d.Spec.Replicas != nil {
		replicas = int(*d.Spec.Replicas)
	}

	strategy, serr := extractStrategy(d.Spec.Strategy)
	if serr != nil {
		return ir.Workload{}, serr
	}

	readiness := extractReadiness(d)
	termination := extractTermination(d)

	w, err := ir.NewWorkload(ir.Workload{
		Kind:        "Deployment",
		Name:        d.Name,
		Namespace:   d.Namespace,
		ServiceName: serviceName,
		Version:     version,
		Replicas:    replicas,
		Strategy:    strategy,
		Readiness:   readiness,
		Termination: termination,
		DependsOn:   dependsOn(d),
	})
	if err != nil {
		return ir.Workload{}, &ParseError{Message: err.Error()}
	}
	return w, nil
}

func extractVersion(d appsv1.Deployment) (string, error) {
	if v := d.Labels[labelVersion]; v != "" {
		return v, nil
	}
	for _, c := range d.Spec.Template.Spec.Containers {
		if c.Image == "" {
			continue
		}
		return imageTag(c.Image), nil
	}
	return "", &ParseError{Message: fmt.Sprintf(
		"Deployment %q: cannot determine version — no %q label and no container image found",
		d.Name, labelVersion)}
}

// imageTag extracts the tag/digest portion of a container image
// reference, e.g. "api:v2" -> "v2", "registry.io/api@sha256:abcd" ->
// "sha256:abcd", "api" (no tag) -> "latest" (Docker's own implicit
// default, applied here for the same reason the Kubernetes rolling-update
// defaults below are applied: it is a well-known, stable upstream
// default, not a RolloutProof guess).
func imageTag(image string) string {
	if i := strings.LastIndex(image, "@"); i != -1 {
		return image[i+1:]
	}
	// A ':' after the last '/' is a tag separator; a ':' before it is
	// part of a registry host:port, not a tag.
	lastSlash := strings.LastIndex(image, "/")
	rest := image
	if lastSlash != -1 {
		rest = image[lastSlash+1:]
	}
	if i := strings.LastIndex(rest, ":"); i != -1 {
		return rest[i+1:]
	}
	return "latest"
}

func extractStrategy(s appsv1.DeploymentStrategy) (ir.RolloutStrategy, error) {
	strategyType := s.Type
	if strategyType == "" {
		strategyType = appsv1.RollingUpdateDeploymentStrategyType
	}

	switch strategyType {
	case appsv1.RecreateDeploymentStrategyType:
		return ir.RolloutStrategy{Type: ir.StrategyRecreate}, nil

	case appsv1.RollingUpdateDeploymentStrategyType:
		// Kubernetes defaults both fields to 25% when the strategy type
		// is RollingUpdate and the RollingUpdate struct itself (or a
		// given field) is unset — a documented upstream API default
		// (apps/v1's defaulting logic), applied here explicitly since
		// unmarshalling raw YAML does not run the API server's own
		// defaulting webhooks.
		surge := ir.IntOrPercent{IsPercent: true, Percent: 25}
		unavailable := ir.IntOrPercent{IsPercent: true, Percent: 25}
		if s.RollingUpdate != nil {
			var err error
			if s.RollingUpdate.MaxSurge != nil {
				surge, err = convertIntOrString(*s.RollingUpdate.MaxSurge)
				if err != nil {
					return ir.RolloutStrategy{}, err
				}
			}
			if s.RollingUpdate.MaxUnavailable != nil {
				unavailable, err = convertIntOrString(*s.RollingUpdate.MaxUnavailable)
				if err != nil {
					return ir.RolloutStrategy{}, err
				}
			}
		}
		return ir.RolloutStrategy{Type: ir.StrategyRollingUpdate, MaxSurge: surge, MaxUnavailable: unavailable}, nil

	default:
		return ir.RolloutStrategy{}, &ParseError{Message: fmt.Sprintf("unrecognized deployment strategy type %q", strategyType)}
	}
}

func convertIntOrString(v intstr.IntOrString) (ir.IntOrPercent, error) {
	if v.Type == intstr.String {
		s := strings.TrimSuffix(v.StrVal, "%")
		n, err := strconv.Atoi(s)
		if err != nil || !strings.HasSuffix(v.StrVal, "%") {
			return ir.IntOrPercent{}, &ParseError{Message: fmt.Sprintf("invalid percentage value %q", v.StrVal)}
		}
		return ir.IntOrPercent{IsPercent: true, Percent: n}, nil
	}
	return ir.IntOrPercent{IntValue: int(v.IntVal)}, nil
}

func extractReadiness(d appsv1.Deployment) ir.ReadinessSpec {
	spec := ir.ReadinessSpec{}
	for _, c := range d.Spec.Template.Spec.Containers {
		if c.ReadinessProbe != nil {
			spec.HasReadinessProbe = true
			spec.InitialDelaySeconds = int(c.ReadinessProbe.InitialDelaySeconds)
			break
		}
	}
	if v, ok := d.Annotations[annotationWaits]; ok {
		if b, err := strconv.ParseBool(v); err == nil {
			spec.WaitsOnDependencies = b
		}
	}
	return spec
}

func extractTermination(d appsv1.Deployment) ir.TerminationSpec {
	spec := ir.TerminationSpec{GracePeriodSeconds: 30} // Kubernetes' own documented pod default
	if d.Spec.Template.Spec.TerminationGracePeriodSeconds != nil {
		spec.GracePeriodSeconds = int(*d.Spec.Template.Spec.TerminationGracePeriodSeconds)
	}
	for _, c := range d.Spec.Template.Spec.Containers {
		if c.Lifecycle != nil && c.Lifecycle.PreStop != nil {
			spec.HasPreStopHook = true
			break
		}
	}
	return spec
}

func dependsOn(d appsv1.Deployment) []string {
	raw, ok := d.Annotations[annotationDepends]
	if !ok || strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
