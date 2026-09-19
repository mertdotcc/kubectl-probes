// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package collect

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// object builds the Unstructured an API server would have returned. It goes
// through JSON so integers arrive as int64, the way the dynamic client
// delivers them and the way the converter expects them.
func object(t *testing.T, manifest string) *unstructured.Unstructured {
	t.Helper()
	data, err := yaml.YAMLToJSON([]byte(manifest))
	if err != nil {
		t.Fatalf("bad fixture manifest: %v", err)
	}
	obj := &unstructured.Unstructured{}
	if err := obj.UnmarshalJSON(data); err != nil {
		t.Fatalf("bad fixture manifest: %v", err)
	}
	return obj
}

func TestPodTemplate(t *testing.T) {
	tests := []struct {
		name          string
		manifest      string
		wantFound     bool
		wantContainer string
		wantProbePath string
	}{
		{
			name: "deployment",
			manifest: `
apiVersion: apps/v1
kind: Deployment
metadata: {name: api, namespace: prod}
spec:
  template:
    spec:
      containers:
      - name: api
        image: api:1
        livenessProbe:
          httpGet: {path: /healthz, port: 8080}
          periodSeconds: 30
`,
			wantFound:     true,
			wantContainer: "api",
			wantProbePath: "/healthz",
		},
		{
			name: "cronjob keeps its template one level deeper",
			manifest: `
apiVersion: batch/v1
kind: CronJob
metadata: {name: nightly, namespace: prod}
spec:
  schedule: "0 2 * * *"
  jobTemplate:
    spec:
      template:
        spec:
          containers:
          - name: backup
            image: backup:1
            readinessProbe:
              httpGet: {path: /ready, port: 9000}
`,
			wantFound:     true,
			wantContainer: "backup",
			wantProbePath: "/ready",
		},
		{
			name: "a pod is its own template",
			manifest: `
apiVersion: v1
kind: Pod
metadata: {name: shell, namespace: prod}
spec:
  containers:
  - name: shell
    image: shell:1
    livenessProbe:
      httpGet: {path: /alive, port: 8080}
`,
			wantFound:     true,
			wantContainer: "shell",
			wantProbePath: "/alive",
		},
		{
			name: "a custom kind that keeps a template where the others do",
			manifest: `
apiVersion: argoproj.io/v1alpha1
kind: Rollout
metadata: {name: checkout, namespace: prod}
spec:
  template:
    spec:
      containers:
      - name: checkout
        image: checkout:1
        readinessProbe:
          httpGet: {path: /ready, port: 8080}
`,
			wantFound:     true,
			wantContainer: "checkout",
			wantProbePath: "/ready",
		},
		{
			name: "a custom kind that keeps none",
			manifest: `
apiVersion: acme.io/v1
kind: Widget
metadata: {name: thing, namespace: prod}
spec:
  replicas: 3
`,
		},
		{
			name: "something else entirely at the template path",
			manifest: `
apiVersion: acme.io/v1
kind: Machine
metadata: {name: node, namespace: prod}
spec:
  template:
    spec:
      diskGiB: 40
`,
		},
		{
			name: "a sidecar alone is still a template",
			manifest: `
apiVersion: apps/v1
kind: StatefulSet
metadata: {name: mesh, namespace: prod}
spec:
  template:
    spec:
      initContainers:
      - name: proxy
        image: proxy:1
        restartPolicy: Always
        readinessProbe:
          httpGet: {path: /healthz/ready, port: 15021}
`,
			wantFound:     true,
			wantContainer: "",
			wantProbePath: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec, found := PodTemplate(object(t, tt.manifest))
			if found != tt.wantFound {
				t.Fatalf("PodTemplate found = %v, want %v", found, tt.wantFound)
			}
			if !found {
				return
			}
			if tt.wantContainer == "" {
				if len(spec.InitContainers) != 1 {
					t.Fatalf("template has %d init containers, want 1", len(spec.InitContainers))
				}
				return
			}
			if len(spec.Containers) != 1 || spec.Containers[0].Name != tt.wantContainer {
				t.Fatalf("template containers = %v, want one named %s", spec.Containers, tt.wantContainer)
			}
			probe := spec.Containers[0].LivenessProbe
			if probe == nil {
				probe = spec.Containers[0].ReadinessProbe
			}
			if probe == nil || probe.HTTPGet == nil || probe.HTTPGet.Path != tt.wantProbePath {
				t.Errorf("probe = %v, want an http probe on %s", probe, tt.wantProbePath)
			}
		})
	}
}

func TestPodTemplateOfNothing(t *testing.T) {
	if _, found := PodTemplate(nil); found {
		t.Error("PodTemplate(nil) found a template")
	}
}
