// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package collect

import (
	"sort"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

// owned is a pod and the workload the owner walk resolved it to. A nil owner
// is a bare pod, which is its own workload.
type owned struct {
	pod   *corev1.Pod
	owner *unstructured.Unstructured
}

// workloadKey is the identity a run groups by. Name alone is not enough: two
// namespaces can hold a deploy/api each, and so can two kinds.
type workloadKey struct {
	groupKind schema.GroupKind
	namespace string
	name      string
}

func ownerKey(obj *unstructured.Unstructured) workloadKey {
	gvk := obj.GroupVersionKind()
	return workloadKey{
		groupKind: gvk.GroupKind(),
		namespace: obj.GetNamespace(),
		name:      obj.GetName(),
	}
}

func podKey(pod *corev1.Pod) workloadKey {
	return workloadKey{
		groupKind: schema.GroupKind{Kind: "Pod"},
		namespace: pod.Namespace,
		name:      pod.Name,
	}
}

// group collects pods under the workloads they belong to and hands each pod
// the failure evidence recorded against it.
//
// The order is fixed here rather than at the point of printing, so the same
// cluster produces the same report twice in a row whatever order the API
// server listed its pods in.
func group(items []owned, events map[types.UID][]corev1.Event) []Workload {
	workloads := map[workloadKey]*Workload{}
	var order []workloadKey

	for _, item := range items {
		key := podKey(item.pod)
		if item.owner != nil {
			key = ownerKey(item.owner)
			// A cluster-scoped owner, which is a Node for a static pod, has
			// no namespace of its own, so the workload takes its pod's: that
			// is the namespace -n narrows to it by.
			if key.namespace == "" {
				key.namespace = item.pod.Namespace
			}
		}

		workload, seen := workloads[key]
		if !seen {
			workload = newWorkload(key, item.owner)
			if item.owner == nil {
				// A bare pod is its own template, the same reading PodTemplate
				// takes of a pod in a manifest, so it has no drift to report
				// rather than drift unknown.
				workload.Template = item.pod.Spec.DeepCopy()
			}
			workloads[key] = workload
			order = append(order, key)
		}
		workload.Pods = append(workload.Pods, Pod{
			Pod:    item.pod,
			Events: events[item.pod.UID],
		})
	}

	out := make([]Workload, 0, len(order))
	for _, key := range order {
		workload := workloads[key]
		sort.Slice(workload.Pods, func(i, j int) bool {
			return workload.Pods[i].Pod.Name < workload.Pods[j].Pod.Name
		})
		out = append(out, *workload)
	}
	sortWorkloads(out)
	return out
}

func newWorkload(key workloadKey, owner *unstructured.Unstructured) *Workload {
	workload := &Workload{
		GroupKind: key.groupKind,
		Name:      key.name,
		Namespace: key.namespace,
		Owner:     owner,
	}
	if template, ok := PodTemplate(owner); ok {
		workload.Template = template
	}
	return workload
}

// keysOf indexes workloads by identity, so a workload already built from the
// pods that belong to it is not added a second time from its own object.
func keysOf(workloads []Workload) map[workloadKey]bool {
	keys := make(map[workloadKey]bool, len(workloads))
	for _, workload := range workloads {
		keys[workloadKey{groupKind: workload.GroupKind, namespace: workload.Namespace, name: workload.Name}] = true
	}
	return keys
}

func sortWorkloads(workloads []Workload) {
	sort.Slice(workloads, func(i, j int) bool {
		a, b := workloads[i], workloads[j]
		switch {
		case a.Namespace != b.Namespace:
			return a.Namespace < b.Namespace
		case a.Name != b.Name:
			return a.Name < b.Name
		case a.GroupKind.Kind != b.GroupKind.Kind:
			return a.GroupKind.Kind < b.GroupKind.Kind
		default:
			return a.GroupKind.Group < b.GroupKind.Group
		}
	})
}
