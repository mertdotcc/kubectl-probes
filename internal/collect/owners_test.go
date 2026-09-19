// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package collect

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
)

// fixtures is a stand-in cluster: the objects an owner walk can read, and a
// tally of how often it asked for them.
type fixtures struct {
	objects map[string]*unstructured.Unstructured
	forbid  map[string]bool
	reads   int
}

func (f *fixtures) get(_ context.Context, gvk schema.GroupVersionKind, namespace, name string) (*unstructured.Unstructured, error) {
	f.reads++
	key := gvk.Kind + "/" + name
	if f.forbid[key] {
		return nil, apierrors.NewForbidden(schema.GroupResource{Resource: gvk.Kind}, name, nil)
	}
	obj, ok := f.objects[key]
	if !ok {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: gvk.Kind}, name)
	}
	return obj, nil
}

func owner(apiVersion, kind, name string, uid types.UID, controller *metav1.OwnerReference) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion(apiVersion)
	obj.SetKind(kind)
	obj.SetName(name)
	obj.SetNamespace("prod")
	obj.SetUID(uid)
	if controller != nil {
		obj.SetOwnerReferences([]metav1.OwnerReference{*controller})
	}
	return obj
}

func controllerRef(apiVersion, kind, name string, uid types.UID) *metav1.OwnerReference {
	return &metav1.OwnerReference{
		APIVersion: apiVersion,
		Kind:       kind,
		Name:       name,
		UID:        uid,
		Controller: ptr.To(true),
	}
}

func pod(name string, uid types.UID, controller *metav1.OwnerReference) *corev1.Pod {
	p := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "prod", UID: uid}}
	if controller != nil {
		p.OwnerReferences = []metav1.OwnerReference{*controller}
	}
	return p
}

// deploymentFixtures is the ordinary chain: pods under a ReplicaSet under a
// Deployment.
func deploymentFixtures() *fixtures {
	deployRef := controllerRef("apps/v1", "Deployment", "api", "deploy-uid")
	return &fixtures{
		objects: map[string]*unstructured.Unstructured{
			"ReplicaSet/api-7d9f4c8b6d": owner("apps/v1", "ReplicaSet", "api-7d9f4c8b6d", "rs-uid", deployRef),
			"Deployment/api":            owner("apps/v1", "Deployment", "api", "deploy-uid", nil),
		},
		forbid: map[string]bool{},
	}
}

func TestTopMostWalksToTheDeployment(t *testing.T) {
	f := deploymentFixtures()
	walker := newOwnerWalker(f.get, &Gaps{})

	got, err := walker.TopMost(context.Background(),
		pod("api-7d9f4c8b6d-2xk9v", "pod-1", controllerRef("apps/v1", "ReplicaSet", "api-7d9f4c8b6d", "rs-uid")))
	if err != nil {
		t.Fatalf("TopMost failed: %v", err)
	}
	if got.GetKind() != "Deployment" || got.GetName() != "api" {
		t.Errorf("TopMost = %s/%s, want Deployment/api", got.GetKind(), got.GetName())
	}
}

func TestTopMostCachesTheChain(t *testing.T) {
	f := deploymentFixtures()
	walker := newOwnerWalker(f.get, &Gaps{})

	// Fifty replicas of one Deployment: the ReplicaSet and the Deployment are
	// each worth exactly one read.
	for i := 0; i < 50; i++ {
		if _, err := walker.TopMost(context.Background(),
			pod("api-7d9f4c8b6d-"+string(rune('a'+i%26)), types.UID("pod-"+string(rune('a'+i%26))),
				controllerRef("apps/v1", "ReplicaSet", "api-7d9f4c8b6d", "rs-uid"))); err != nil {
			t.Fatalf("TopMost failed: %v", err)
		}
	}
	if f.reads != 2 {
		t.Errorf("read the API server %d times for 50 pods of one Deployment, want 2", f.reads)
	}
}

func TestTopMostBarePodHasNoOwner(t *testing.T) {
	f := deploymentFixtures()
	walker := newOwnerWalker(f.get, &Gaps{})

	got, err := walker.TopMost(context.Background(), pod("shell", "pod-shell", nil))
	if err != nil {
		t.Fatalf("TopMost failed: %v", err)
	}
	if got != nil {
		t.Errorf("TopMost = %s/%s, want nil for a pod that owns itself", got.GetKind(), got.GetName())
	}
	if f.reads != 0 {
		t.Errorf("read the API server %d times for a bare pod, want 0", f.reads)
	}
}

func TestTopMostStopsAtAnUnreadableOwner(t *testing.T) {
	tests := []struct {
		name       string
		forbid     string
		wantKind   string
		wantName   string
		wantGapFor string
	}{
		{
			name:       "forbidden owner is recorded and named",
			forbid:     "Deployment/api",
			wantKind:   "Deployment",
			wantName:   "api",
			wantGapFor: "Deployment",
		},
		{
			name:     "missing owner still names the workload",
			forbid:   "",
			wantKind: "Deployment",
			wantName: "api",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := deploymentFixtures()
			if tt.forbid != "" {
				f.forbid[tt.forbid] = true
			} else {
				delete(f.objects, "Deployment/api")
			}
			gaps := &Gaps{}
			walker := newOwnerWalker(f.get, gaps)

			got, err := walker.TopMost(context.Background(),
				pod("api-7d9f4c8b6d-2xk9v", "pod-1", controllerRef("apps/v1", "ReplicaSet", "api-7d9f4c8b6d", "rs-uid")))
			if err != nil {
				t.Fatalf("TopMost failed: %v", err)
			}
			if got.GetKind() != tt.wantKind || got.GetName() != tt.wantName {
				t.Errorf("TopMost = %s/%s, want %s/%s", got.GetKind(), got.GetName(), tt.wantKind, tt.wantName)
			}
			if _, ok := PodTemplate(got); ok {
				t.Error("an owner that could not be read must not offer a template")
			}

			if tt.wantGapFor == "" {
				if len(gaps.Owners) != 0 {
					t.Errorf("Gaps.Owners = %v, want none", gaps.Owners)
				}
				return
			}
			if len(gaps.Owners) != 1 || gaps.Owners[0].Kind != tt.wantGapFor {
				t.Errorf("Gaps.Owners = %v, want one entry for %s", gaps.Owners, tt.wantGapFor)
			}
		})
	}
}

func TestTopMostSurvivesAnOwnerCycle(t *testing.T) {
	// A cluster should never produce this, but a walk that loops forever is a
	// worse failure than an error.
	f := &fixtures{
		objects: map[string]*unstructured.Unstructured{
			"Widget/a": owner("acme.io/v1", "Widget", "a", "a-uid", controllerRef("acme.io/v1", "Widget", "b", "b-uid")),
			"Widget/b": owner("acme.io/v1", "Widget", "b", "b-uid", controllerRef("acme.io/v1", "Widget", "a", "a-uid")),
		},
		forbid: map[string]bool{},
	}
	walker := newOwnerWalker(f.get, &Gaps{})

	_, err := walker.TopMost(context.Background(),
		pod("widget-0", "pod-1", controllerRef("acme.io/v1", "Widget", "a", "a-uid")))
	if err == nil {
		t.Fatal("TopMost on a cycle returned no error")
	}
}

func TestForbidOwnerRecordsEachKindOnce(t *testing.T) {
	gaps := &Gaps{}
	gaps.forbidOwner(schema.GroupKind{Group: "apps", Kind: "Deployment"})
	gaps.forbidOwner(schema.GroupKind{Group: "apps", Kind: "Deployment"})
	gaps.forbidOwner(schema.GroupKind{Group: "argoproj.io", Kind: "Rollout"})

	if len(gaps.Owners) != 2 {
		t.Errorf("Gaps.Owners = %v, want one entry per kind", gaps.Owners)
	}
}
