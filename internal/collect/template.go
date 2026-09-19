// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package collect

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// templatePaths are where a workload keeps the pod spec it stamps out, in the
// order they are tried. A CronJob hides one template inside another; every
// other kind this tool knows, and every custom kind worth trying, puts it in
// the same place.
var templatePaths = [][]string{
	{"spec", "template", "spec"},
	{"spec", "jobTemplate", "spec", "template", "spec"},
}

// PodTemplate returns the pod spec a workload object stamps its pods out of.
//
// It reports false for a kind that carries no template at all, which is how an
// unreadable owner ends up with drift unknown rather than drift wrongly
// claimed. Per ADR-0001 the template is the secondary source: it explains a
// workload with no pods, and it is what a running pod is compared against.
//
// A Pod object is its own template. That is what makes kubectl probes -f
// pod.yaml show anything, and it is the same reading ADR-0001 takes of a
// running pod.
func PodTemplate(obj *unstructured.Unstructured) (*corev1.PodSpec, bool) {
	if obj == nil {
		return nil, false
	}
	paths := templatePaths
	if obj.GroupVersionKind().GroupKind() == (schema.GroupKind{Kind: "Pod"}) {
		paths = [][]string{{"spec"}}
	}
	for _, path := range paths {
		raw, found, err := unstructured.NestedMap(obj.Object, path...)
		if err != nil || !found {
			continue
		}
		spec := &corev1.PodSpec{}
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(raw, spec); err != nil {
			// The field is there but is not a pod spec. That is a kind whose
			// template this tool cannot read, not an error worth a run.
			continue
		}
		if len(spec.Containers) == 0 && len(spec.InitContainers) == 0 {
			// A pod spec with no containers is not a pod spec. Some other
			// kind keeps something else at this path.
			continue
		}
		return spec, true
	}
	return nil, false
}
