// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package collect

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

// maxOwnerDepth bounds the walk. Real chains are two links at most, and an
// ownerReference cycle is a cluster bug this tool should survive rather than
// spin on.
const maxOwnerDepth = 10

// objectGetter reads one object. The walk takes it as a function so its logic
// can be exercised against fixture objects instead of a cluster.
type objectGetter func(ctx context.Context, gvk schema.GroupVersionKind, namespace, name string) (*unstructured.Unstructured, error)

// ownerWalker resolves a pod's top-most owner: the Deployment behind a
// ReplicaSet, the CronJob behind a Job, or nothing at all for a bare pod.
//
// Results are cached by the UID of every object on the chain, so a
// fifty-replica Deployment costs one ReplicaSet read and one Deployment read
// however many pods point at them.
type ownerWalker struct {
	get  objectGetter
	gaps *Gaps
	// top maps the UID of any object on a chain to the top-most owner that
	// chain ends at.
	top map[types.UID]*unstructured.Unstructured
}

func newOwnerWalker(get objectGetter, gaps *Gaps) *ownerWalker {
	return &ownerWalker{get: get, gaps: gaps, top: map[types.UID]*unstructured.Unstructured{}}
}

// TopMost returns the workload an object belongs to, or nil when the object
// has no controller and is its own workload.
//
// It takes any object rather than a pod because the same question is worth
// asking of a ReplicaSet read out of a manifest: a ReplicaSet whose Deployment
// is there too is not a workload, it is part of one.
//
// An owner that cannot be read stops the walk: the last owner reference is
// reported as the workload, with the gap recorded, because a pod under an
// unreadable Deployment is better described by its ReplicaSet than dropped.
func (w *ownerWalker) TopMost(ctx context.Context, obj metav1.Object) (*unstructured.Unstructured, error) {
	ref := metav1.GetControllerOf(obj)
	if ref == nil {
		return nil, nil
	}
	return w.resolve(ctx, *ref, obj.GetNamespace())
}

func (w *ownerWalker) resolve(ctx context.Context, ref metav1.OwnerReference, namespace string) (*unstructured.Unstructured, error) {
	// Every UID visited on the way up ends at the same top-most owner, so they
	// are all worth remembering.
	var chain []types.UID

	for depth := 0; depth < maxOwnerDepth; depth++ {
		if top, ok := w.top[ref.UID]; ok {
			w.memoize(chain, top)
			return top, nil
		}
		chain = append(chain, ref.UID)

		gvk := schema.FromAPIVersionAndKind(ref.APIVersion, ref.Kind)
		obj, err := w.get(ctx, gvk, namespace, ref.Name)
		switch {
		case apierrors.IsForbidden(err):
			w.gaps.forbidOwner(gvk.GroupKind())
			fallthrough
		case apierrors.IsNotFound(err):
			// The owner is gone or off limits. The reference still names the
			// workload, so keep what it says and stop climbing.
			stub := ownerStub(ref, namespace)
			w.memoize(chain, stub)
			return stub, nil
		case err != nil:
			return nil, fmt.Errorf("reading owner %s %s/%s: %w", ref.Kind, namespace, ref.Name, err)
		}

		next := metav1.GetControllerOf(obj)
		if next == nil {
			w.memoize(chain, obj)
			return obj, nil
		}
		ref = *next
	}
	return nil, fmt.Errorf("owner chain of %s/%s is more than %d deep, which means it has a cycle",
		namespace, ref.Name, maxOwnerDepth)
}

func (w *ownerWalker) memoize(chain []types.UID, top *unstructured.Unstructured) {
	for _, uid := range chain {
		w.top[uid] = top
	}
}

// ownerStub is what is known about an owner that could not be read: enough to
// name it, and nothing that would let a template be read off it.
func ownerStub(ref metav1.OwnerReference, namespace string) *unstructured.Unstructured {
	stub := &unstructured.Unstructured{}
	stub.SetAPIVersion(ref.APIVersion)
	stub.SetKind(ref.Kind)
	stub.SetName(ref.Name)
	stub.SetNamespace(namespace)
	stub.SetUID(ref.UID)
	return stub
}
