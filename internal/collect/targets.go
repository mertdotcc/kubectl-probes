// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package collect

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/cli-runtime/pkg/resource"

	"github.com/mertdotcc/kubectl-probes/internal/model"
)

// targets is what the positional argument named. A run without one targets
// everything the pod list turns up.
type targets struct {
	all bool
	// objects are the named workloads, by identity, so a pod can be matched
	// against them without another API call.
	objects map[workloadKey]*unstructured.Unstructured
}

// wants reports whether a pod belongs to something the user asked about. A pod
// named directly is kept even when it belongs to a workload, because asking
// about one pod is asking about one pod.
func (t *targets) wants(pod *corev1.Pod, owner *unstructured.Unstructured) bool {
	if t.all {
		return true
	}
	if owner != nil {
		if _, ok := t.objects[ownerKey(owner)]; ok {
			return true
		}
	}
	_, ok := t.objects[podKey(pod)]
	return ok
}

// withoutPods returns the named workloads that no pod resolved to. A workload
// scaled to zero has nothing running to describe, and saying so is the point:
// it is exactly the case a pods-only reading would hide.
func (t *targets) withoutPods(found []Workload) []Workload {
	if t.all {
		return nil
	}
	has := make(map[workloadKey]bool, len(found))
	for _, workload := range found {
		has[workloadKey{groupKind: workload.GroupKind, namespace: workload.Namespace, name: workload.Name}] = true
	}

	var extra []Workload
	for key, obj := range t.objects {
		if has[key] {
			continue
		}
		if workload, ok := workloadFrom(obj); ok {
			extra = append(extra, workload)
		}
	}
	return extra
}

// resolveTargets turns the positional argument into the workloads it names,
// through the same Builder kubectl get uses, so this plugin accepts exactly
// what kubectl accepts.
func resolveTargets(o Options, namespace string) (*targets, error) {
	if len(o.Args) == 0 {
		return &targets{all: true}, nil
	}

	infos, err := lookup(o, namespace, o.Args)
	if err == nil {
		return targetsOf(infos), nil
	}
	// A single word that is not a resource type is a name: kubectl probes api
	// should find deploy/api without being told which kind it is.
	if len(o.Args) == 1 && !strings.Contains(o.Args[0], "/") && isUnknownType(err) {
		return resolveBareName(o, namespace, o.Args[0])
	}
	return nil, err
}

// resolveBareName tries a name against every workload kind and insists the
// user disambiguate when more than one answers, rather than guessing which
// deploy/api or sts/api was meant.
func resolveBareName(o Options, namespace, name string) (*targets, error) {
	var found []*resource.Info
	for _, kind := range knownKinds {
		infos, err := lookup(o, namespace, []string{kind + "/" + name})
		if err != nil {
			// Not there, or not a kind this cluster serves. Either way the
			// next kind is the interesting one.
			continue
		}
		found = append(found, infos...)
	}

	switch len(found) {
	case 0:
		return nil, fmt.Errorf("no %s named %q found", joinKinds(knownKinds), name)
	case 1:
		return targetsOf(found), nil
	default:
		names := make([]string, 0, len(found))
		for _, info := range found {
			gvk := info.Object.GetObjectKind().GroupVersionKind()
			names = append(names, model.DisplayName(gvk.Group, gvk.Kind, info.Name))
		}
		return nil, fmt.Errorf("%q is ambiguous: it matches %s. Name one of them instead",
			name, joinKinds(names))
	}
}

func lookup(o Options, namespace string, args []string) ([]*resource.Info, error) {
	return resource.NewBuilder(o.ConfigFlags).
		Unstructured().
		NamespaceParam(namespace).DefaultNamespace().
		AllNamespaces(o.AllNamespaces).
		LabelSelectorParam(o.Selector).
		ResourceTypeOrNameArgs(true, args...).
		Latest().
		Flatten().
		ContinueOnError().
		Do().Infos()
}

func targetsOf(infos []*resource.Info) *targets {
	t := &targets{objects: map[workloadKey]*unstructured.Unstructured{}}
	for _, info := range infos {
		obj, ok := info.Object.(*unstructured.Unstructured)
		if !ok {
			continue
		}
		t.objects[ownerKey(obj)] = obj
	}
	return t
}

// isUnknownType reports whether the Builder rejected the argument because the
// cluster has no such resource type, which is the answer that means "that was
// a name, not a type".
func isUnknownType(err error) bool {
	if meta.IsNoMatchError(err) {
		return true
	}
	// The Builder wraps its mapper errors, and this is the sentence it wraps
	// them in.
	return strings.Contains(err.Error(), "server doesn't have a resource type")
}
