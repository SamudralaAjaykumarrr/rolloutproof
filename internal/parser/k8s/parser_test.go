package k8s

import (
	"strings"
	"testing"

	"github.com/SamudralaAjaykumarrr/rolloutproof/internal/ir"
)

const rollingUpdateManifest = `
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
    rollingUpdate:
      maxSurge: 1
      maxUnavailable: 0
  selector:
    matchLabels:
      app: api
  template:
    metadata:
      labels:
        app: api
    spec:
      containers:
        - name: api
          image: registry.example.com/api:v2
          readinessProbe:
            httpGet:
              path: /healthz
              port: 8080
            initialDelaySeconds: 5
`

func TestParseDeployment_RollingUpdate(t *testing.T) {
	w, err := ParseDeployment([]byte(rollingUpdateManifest))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w.ServiceName != "api" || w.Version != "v2" {
		t.Fatalf("unexpected identity: %+v", w)
	}
	if w.Replicas != 3 {
		t.Fatalf("expected 3 replicas, got %d", w.Replicas)
	}
	if w.Strategy.Type != ir.StrategyRollingUpdate {
		t.Fatalf("expected RollingUpdate strategy, got %v", w.Strategy.Type)
	}
	if w.Strategy.MaxSurge.IntValue != 1 || w.Strategy.MaxSurge.IsPercent {
		t.Fatalf("unexpected maxSurge: %+v", w.Strategy.MaxSurge)
	}
	if w.Strategy.MaxUnavailable.IntValue != 0 {
		t.Fatalf("unexpected maxUnavailable: %+v", w.Strategy.MaxUnavailable)
	}
	if !w.Readiness.HasReadinessProbe || w.Readiness.InitialDelaySeconds != 5 {
		t.Fatalf("unexpected readiness: %+v", w.Readiness)
	}
	if !w.Strategy.AllowsCoexistence(w.Replicas) {
		t.Fatalf("expected coexistence to be possible with maxSurge=1")
	}
}

func TestParseDeployment_DefaultedValues(t *testing.T) {
	manifest := `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
spec:
  selector:
    matchLabels:
      app: api
  template:
    metadata:
      labels:
        app: api
    spec:
      containers:
        - name: api
          image: api:v1
`
	w, err := ParseDeployment([]byte(manifest))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w.ServiceName != "api" {
		t.Fatalf("expected serviceName to default to deployment name, got %q", w.ServiceName)
	}
	if w.Version != "v1" {
		t.Fatalf("expected version to default to image tag, got %q", w.Version)
	}
	if w.Replicas != 1 {
		t.Fatalf("expected replicas to default to 1, got %d", w.Replicas)
	}
	if w.Strategy.Type != ir.StrategyRollingUpdate {
		t.Fatalf("expected strategy to default to RollingUpdate, got %v", w.Strategy.Type)
	}
	if !w.Strategy.MaxSurge.IsPercent || w.Strategy.MaxSurge.Percent != 25 {
		t.Fatalf("expected default maxSurge of 25%%, got %+v", w.Strategy.MaxSurge)
	}
	if !w.Strategy.MaxUnavailable.IsPercent || w.Strategy.MaxUnavailable.Percent != 25 {
		t.Fatalf("expected default maxUnavailable of 25%%, got %+v", w.Strategy.MaxUnavailable)
	}
	if w.Termination.GracePeriodSeconds != 30 {
		t.Fatalf("expected default termination grace period of 30s, got %d", w.Termination.GracePeriodSeconds)
	}
}

func TestParseDeployment_MaxSurgePercentage(t *testing.T) {
	manifest := `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
spec:
  replicas: 4
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxSurge: 50%
      maxUnavailable: 25%
  selector:
    matchLabels: {app: api}
  template:
    metadata:
      labels: {app: api}
    spec:
      containers:
        - name: api
          image: api:v1
`
	w, err := ParseDeployment([]byte(manifest))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !w.Strategy.MaxSurge.IsPercent || w.Strategy.MaxSurge.Percent != 50 {
		t.Fatalf("unexpected maxSurge: %+v", w.Strategy.MaxSurge)
	}
	if !w.Strategy.MaxUnavailable.IsPercent || w.Strategy.MaxUnavailable.Percent != 25 {
		t.Fatalf("unexpected maxUnavailable: %+v", w.Strategy.MaxUnavailable)
	}
	if got := w.Strategy.MaxSurge.Resolve(4); got != 2 {
		t.Fatalf("expected 50%% of 4 to resolve to 2, got %d", got)
	}
}

func TestParseDeployment_Recreate(t *testing.T) {
	manifest := `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: batch-worker
spec:
  strategy:
    type: Recreate
  selector:
    matchLabels: {app: batch-worker}
  template:
    metadata:
      labels: {app: batch-worker}
    spec:
      containers:
        - name: worker
          image: batch-worker:v2
`
	w, err := ParseDeployment([]byte(manifest))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w.Strategy.Type != ir.StrategyRecreate {
		t.Fatalf("expected Recreate strategy, got %v", w.Strategy.Type)
	}
	if w.Strategy.AllowsCoexistence(w.Replicas) {
		t.Fatalf("Recreate must never allow coexistence")
	}
}

func TestParseDeployment_MalformedYAML(t *testing.T) {
	_, err := ParseDeployment([]byte("this: is: not: valid: yaml: [["))
	if err == nil {
		t.Fatalf("expected error for malformed YAML")
	}
}

func TestParseDeployment_MissingName(t *testing.T) {
	manifest := `
apiVersion: apps/v1
kind: Deployment
spec:
  selector: {matchLabels: {app: api}}
  template:
    metadata: {labels: {app: api}}
    spec:
      containers: [{name: api, image: "api:v1"}]
`
	_, err := ParseDeployment([]byte(manifest))
	if err == nil {
		t.Fatalf("expected error for missing metadata.name")
	}
}

func TestParseDeployment_NoImageOrVersionLabel(t *testing.T) {
	manifest := `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
spec:
  selector: {matchLabels: {app: api}}
  template:
    metadata: {labels: {app: api}}
    spec:
      containers: []
`
	_, err := ParseDeployment([]byte(manifest))
	if err == nil {
		t.Fatalf("expected error when no version can be determined")
	}
}

func TestParseDeployment_WrongKind(t *testing.T) {
	manifest := `
apiVersion: v1
kind: Service
metadata:
  name: api
spec:
  selector: {app: api}
`
	_, err := ParseDeployment([]byte(manifest))
	if err == nil {
		t.Fatalf("expected error when ParseDeployment is given a non-Deployment kind directly")
	}
}

func TestParseAll_MultiDocumentSkipsUnsupportedResources(t *testing.T) {
	manifest := `
apiVersion: v1
kind: Service
metadata:
  name: api
spec:
  selector: {app: api}
---
` + rollingUpdateManifest + `
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: api-config
`
	workloads, skipped, err := ParseAll([]byte(manifest))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(workloads) != 1 {
		t.Fatalf("expected 1 workload, got %d", len(workloads))
	}
	if len(skipped) != 2 {
		t.Fatalf("expected 2 skipped resources, got %d: %+v", len(skipped), skipped)
	}
	kinds := map[string]bool{}
	for _, s := range skipped {
		kinds[s.Kind] = true
	}
	if !kinds["Service"] || !kinds["ConfigMap"] {
		t.Fatalf("expected Service and ConfigMap to be recorded as skipped, got %+v", skipped)
	}
}

func TestParseAll_NoDeploymentAtAll(t *testing.T) {
	manifest := `
apiVersion: v1
kind: ConfigMap
metadata:
  name: api-config
`
	workloads, skipped, err := ParseAll([]byte(manifest))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(workloads) != 0 {
		t.Fatalf("expected no workloads, got %+v", workloads)
	}
	if len(skipped) != 1 {
		t.Fatalf("expected 1 skipped resource, got %+v", skipped)
	}
}

func TestParseAll_PropagatesDeploymentErrors(t *testing.T) {
	manifest := `
apiVersion: apps/v1
kind: Deployment
metadata: {}
spec:
  selector: {matchLabels: {app: api}}
  template:
    metadata: {labels: {app: api}}
    spec:
      containers: [{name: api, image: "api:v1"}]
`
	_, _, err := ParseAll([]byte(manifest))
	if err == nil {
		t.Fatalf("expected an error to propagate from a malformed Deployment document")
	}
}

func TestParseDeployment_DependsOnAnnotation(t *testing.T) {
	manifest := `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: checkout
  annotations:
    rolloutproof.dev/depends-on: "payments, auth"
    rolloutproof.dev/waits-on-dependencies: "true"
spec:
  selector: {matchLabels: {app: checkout}}
  template:
    metadata: {labels: {app: checkout}}
    spec:
      containers: [{name: checkout, image: "checkout:v1"}]
`
	w, err := ParseDeployment([]byte(manifest))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(w.DependsOn) != 2 || w.DependsOn[0] != "payments" || w.DependsOn[1] != "auth" {
		t.Fatalf("unexpected DependsOn: %+v", w.DependsOn)
	}
	if !w.Readiness.WaitsOnDependencies {
		t.Fatalf("expected WaitsOnDependencies to be true")
	}
}

func TestImageTag(t *testing.T) {
	cases := map[string]string{
		"api:v2":                               "v2",
		"registry.example.com:5000/api:v2":     "v2",
		"registry.example.com/team/api:v2":     "v2",
		"api":                                  "latest",
		"registry.example.com/api@sha256:abcd": "sha256:abcd",
		"registry.example.com:5000/api":        "latest",
	}
	for image, want := range cases {
		if got := imageTag(image); got != want {
			t.Errorf("imageTag(%q) = %q, want %q", image, got, want)
		}
	}
}

func TestParseDeployment_InvalidPercentage(t *testing.T) {
	manifest := `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
spec:
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxSurge: "notapercent"
  selector: {matchLabels: {app: api}}
  template:
    metadata: {labels: {app: api}}
    spec:
      containers: [{name: api, image: "api:v1"}]
`
	_, err := ParseDeployment([]byte(manifest))
	if err == nil || !strings.Contains(err.Error(), "percentage") {
		t.Fatalf("expected a percentage-related error, got %v", err)
	}
}
