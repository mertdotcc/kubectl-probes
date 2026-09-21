// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package collect

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestGroup(t *testing.T) {
	deployment := object(t, `
apiVersion: apps/v1
kind: Deployment
metadata: {name: api, namespace: prod, uid: deploy-uid}
spec:
  template:
    spec:
      containers:
      - name: api
        image: api:1
        livenessProbe:
          httpGet: {path: /healthz, port: 8080}
`)

	first := pod("api-7d9f4c8b6d-2xk9v", "uid-1", nil)
	second := pod("api-7d9f4c8b6d-0aaaa", "uid-2", nil)
	bare := pod("shell", "uid-3", nil)
	third := pod("api-7d9f4c8b6d-3zzzz", "uid-4", nil)

	events := map[types.UID][]corev1.Event{
		"uid-1": {event("Unhealthy", "Pod", "api-7d9f4c8b6d-2xk9v", "uid-1", "Liveness probe failed")},
	}

	got := group([]owned{
		{pod: first, owner: deployment},
		{pod: bare},
		{pod: third, owner: deployment},
		{pod: second, owner: deployment},
	}, events)

	if len(got) != 2 {
		t.Fatalf("group returned %d workloads, want 2", len(got))
	}

	// Sorted by namespace first: prod's deploy/api, then the bare pod, which
	// sorts after it by name.
	api, shell := got[0], got[1]
	if api.Name != "api" || api.GroupKind.Kind != "Deployment" {
		t.Fatalf("first workload = %s/%s, want the Deployment", api.GroupKind.Kind, api.Name)
	}
	if shell.Name != "shell" || shell.GroupKind.Kind != "Pod" {
		t.Fatalf("second workload = %s/%s, want the bare pod", shell.GroupKind.Kind, shell.Name)
	}

	if len(api.Pods) != 3 {
		t.Errorf("deploy/api has %d pods, want 3", len(api.Pods))
	}
	if api.Pods[0].Pod.Name != "api-7d9f4c8b6d-0aaaa" {
		t.Errorf("pods are not sorted by name: first is %s", api.Pods[0].Pod.Name)
	}
	if api.Template == nil || len(api.Template.Containers) != 1 {
		t.Error("the workload did not pick up its owner's template")
	}
	if shell.Template != nil {
		t.Error("a bare pod has no template to compare against")
	}

	var withEvents int
	for _, p := range api.Pods {
		withEvents += len(p.Events)
	}
	if withEvents != 1 {
		t.Errorf("the workload carries %d events, want the one recorded against uid-1", withEvents)
	}
}

func TestGroupSeparatesNamespaces(t *testing.T) {
	deployment := object(t, `
apiVersion: apps/v1
kind: Deployment
metadata: {name: api, namespace: prod, uid: deploy-uid}
spec:
  template:
    spec:
      containers:
      - name: api
        image: api:1
`)
	inDev := object(t, `
apiVersion: apps/v1
kind: Deployment
metadata: {name: api, namespace: dev, uid: deploy-uid-dev}
spec:
  template:
    spec:
      containers:
      - name: api
        image: api:1
`)

	devPod := pod("api-dev", "uid-dev", nil)
	devPod.Namespace = "dev"

	got := group([]owned{
		{pod: pod("api-prod", "uid-prod", nil), owner: deployment},
		{pod: devPod, owner: inDev},
	}, nil)

	if len(got) != 2 {
		t.Fatalf("group returned %d workloads, want one per namespace", len(got))
	}
	if got[0].Namespace != "dev" || got[1].Namespace != "prod" {
		t.Errorf("workloads are not sorted by namespace: %s then %s", got[0].Namespace, got[1].Namespace)
	}
}

// A static pod's owner is its Node, which has no namespace, while the pod
// itself is in one. The workload takes the pod's, because that is the
// namespace -n narrows to it by.
func TestGroupGivesAClusterScopedOwnerItsPodsNamespace(t *testing.T) {
	node := object(t, `
apiVersion: v1
kind: Node
metadata: {name: control-plane, uid: node-uid}
`)

	etcd := pod("etcd-control-plane", "uid-etcd", nil)
	etcd.Namespace = "kube-system"
	other := pod("agent-control-plane", "uid-agent", nil)
	other.Namespace = "monitoring"

	got := group([]owned{
		{pod: etcd, owner: node},
		{pod: other, owner: node},
	}, nil)

	if len(got) != 2 {
		t.Fatalf("group returned %d workloads, want one per namespace the Node's pods are in", len(got))
	}
	if got[0].Namespace != "kube-system" || got[1].Namespace != "monitoring" {
		t.Errorf("namespaces = %q, %q, want kube-system, monitoring", got[0].Namespace, got[1].Namespace)
	}
	for _, w := range got {
		if w.GroupKind.Kind != "Node" || w.Name != "control-plane" {
			t.Errorf("workload = %s/%s, want the Node", w.GroupKind.Kind, w.Name)
		}
	}
}
