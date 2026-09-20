// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package collect

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/cli-runtime/pkg/resource"
)

// The kinds the manifest path reads as something other than a workload: the
// pods a workload runs, and the failure evidence recorded against them.
var (
	podKind = schema.GroupKind{Kind: "Pod"}
	// Only the core Event is read. The events.k8s.io Event keeps the same
	// facts under different names, and a file holding one is not worth
	// guessing at.
	eventKind = schema.GroupKind{Kind: "Event"}
)

// collectManifests reads workloads out of -f files without contacting a
// cluster, so an unapplied manifest can be inspected before it is applied and
// a saved dump can be read back later.
//
// The files are one cluster as far as this function is concerned: the same
// owner reduction runs over them that runs over what the API server returns,
// so a dump of pods, ReplicaSets, Deployments, and events reads exactly the
// way the cluster it came from does. A file holding a single Deployment and
// nothing else still produces the workload it describes, with no pods.
func collectManifests(ctx context.Context, o Options) (*Result, error) {
	defer traced("reading manifests")()

	set, err := readManifests(o)
	if err != nil {
		return nil, err
	}

	result := &Result{}
	walker := newOwnerWalker(set.get, &result.Gaps)

	items := make([]owned, 0, len(set.pods))
	for _, pod := range set.pods {
		owner, err := walker.TopMost(ctx, pod)
		if err != nil {
			return nil, err
		}
		items = append(items, owned{pod: pod, owner: owner})
	}
	result.Workloads = group(items, eventsByPod(set.events))

	// A workload whose pods are not in the files is still a workload: it is
	// a manifest that has not been applied, or one scaled to zero, and its
	// template is the only thing there is to report.
	extra, err := set.workloadsWithoutPods(ctx, walker, result.Workloads)
	if err != nil {
		return nil, err
	}
	result.Workloads = append(result.Workloads, extra...)
	sortWorkloads(result.Workloads)
	return result, nil
}

// fileSet is what the -f files held, split the way this package reads them and
// indexed the way the owner walk asks for objects.
type fileSet struct {
	objects map[objectKey]*unstructured.Unstructured
	// owners are the objects that are neither pods nor events, in the order
	// the files wrote them, so the same files produce the same report.
	owners []*unstructured.Unstructured
	pods   []*corev1.Pod
	events []corev1.Event
}

// objectKey names one object. The version is deliberately not part of it: a
// manifest may write the same object at a different version than the owner
// reference that points at it.
type objectKey struct {
	groupKind schema.GroupKind
	namespace string
	name      string
}

// readManifests runs the files through the same Builder kubectl get uses, so
// this plugin reads exactly the files kubectl reads. Local keeps it off the
// network: a manifest that has not been applied has no cluster to ask.
func readManifests(o Options) (*fileSet, error) {
	namespace, _, err := o.ConfigFlags.ToRawKubeConfigLoader().Namespace()
	if err != nil {
		return nil, err
	}

	infos, err := resource.NewBuilder(o.ConfigFlags).
		Unstructured().
		NamespaceParam(namespace).DefaultNamespace().
		FilenameParam(false, &resource.FilenameOptions{Filenames: o.Filenames}).
		Local().
		Flatten().
		ContinueOnError().
		Do().Infos()
	if err != nil {
		return nil, err
	}

	set := &fileSet{objects: map[objectKey]*unstructured.Unstructured{}}
	for _, info := range infos {
		obj, ok := info.Object.(*unstructured.Unstructured)
		if !ok {
			continue
		}
		set.add(obj)
	}
	return set, nil
}

func (s *fileSet) add(obj *unstructured.Unstructured) {
	groupKind := obj.GroupVersionKind().GroupKind()
	s.objects[keyOf(obj)] = obj

	switch groupKind {
	case podKind:
		pod := &corev1.Pod{}
		if convert(obj, pod) {
			s.pods = append(s.pods, pod)
		}
	case eventKind:
		event := &corev1.Event{}
		if convert(obj, event) {
			s.events = append(s.events, *event)
		}
	default:
		// A Service or a ConfigMap in the same file is not an error; it
		// simply has no probes to report, and PodTemplate says so later.
		s.owners = append(s.owners, obj)
	}
}

// get is the owner walk's window onto the files. An owner the files do not
// hold is not found, which is what makes the walk stop at the last reference
// it can name rather than fail.
func (s *fileSet) get(_ context.Context, gvk schema.GroupVersionKind, namespace, name string) (*unstructured.Unstructured, error) {
	key := objectKey{groupKind: gvk.GroupKind(), namespace: namespace, name: name}
	if obj, ok := s.objects[key]; ok {
		return obj, nil
	}
	return nil, apierrors.NewNotFound(schema.GroupResource{Group: gvk.Group, Resource: gvk.Kind}, name)
}

// workloadsWithoutPods turns the objects no pod resolved to into workloads of
// their own.
//
// An object whose top-most owner is also in the files is left out: a
// ReplicaSet beside its Deployment is part of that Deployment and never a
// workload. A ReplicaSet whose Deployment is not in the files is all the files
// have to say, so it stands as a workload itself.
func (s *fileSet) workloadsWithoutPods(ctx context.Context, walker *ownerWalker, found []Workload) ([]Workload, error) {
	has := keysOf(found)

	var extra []Workload
	for _, obj := range s.owners {
		top, err := walker.TopMost(ctx, obj)
		if err != nil {
			return nil, err
		}
		if top != nil && s.holds(top) {
			continue
		}
		workload, ok := workloadFrom(obj)
		if !ok {
			continue
		}
		key := workloadKey{groupKind: workload.GroupKind, namespace: workload.Namespace, name: workload.Name}
		if has[key] {
			continue
		}
		has[key] = true
		extra = append(extra, workload)
	}
	return extra, nil
}

func (s *fileSet) holds(obj *unstructured.Unstructured) bool {
	_, ok := s.objects[keyOf(obj)]
	return ok
}

func keyOf(obj *unstructured.Unstructured) objectKey {
	return objectKey{
		groupKind: obj.GroupVersionKind().GroupKind(),
		namespace: obj.GetNamespace(),
		name:      obj.GetName(),
	}
}

// convert reads a typed object out of what the Builder returned, and reports
// whether it is one. A file that writes a Pod this tool cannot read is skipped
// for the same reason a ConfigMap is: there is nothing in it to report.
func convert(obj *unstructured.Unstructured, into any) bool {
	return runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, into) == nil
}

// workloadFrom turns an object the Builder returned into a workload with no
// pods: what an unapplied manifest and a scaled-to-zero workload have to offer.
func workloadFrom(obj *unstructured.Unstructured) (Workload, bool) {
	template, ok := PodTemplate(obj)
	if !ok {
		return Workload{}, false
	}
	gvk := obj.GroupVersionKind()
	return Workload{
		GroupKind: gvk.GroupKind(),
		Name:      obj.GetName(),
		Namespace: obj.GetNamespace(),
		Owner:     obj,
		Template:  template,
	}, true
}
