// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package collect

import (
	"context"
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
)

// scope is what a run covers: the namespace to read, the pod labels to read it
// by, and the workloads the positional argument named.
type scope struct {
	namespace string
	selector  string
	targets   *targets
}

// resolveScope works out what the command line asked for before anything is
// read. Resolving the argument first means a typo in it fails before a full
// pod list is paid for.
func resolveScope(o Options) (*scope, error) {
	namespace, _, err := o.ConfigFlags.ToRawKubeConfigLoader().Namespace()
	if err != nil {
		return nil, err
	}
	if o.AllNamespaces {
		namespace = metav1.NamespaceAll
	}

	targets, err := resolveTargets(o, namespace)
	if err != nil {
		return nil, err
	}
	return &scope{namespace: namespace, selector: o.Selector, targets: targets}, nil
}

// reducer turns the pods and events of a scope into Workloads: the owner walk
// up to the top-most owner, the grouping, and the named workloads no pod
// resolved to.
//
// Collect makes one and uses it once.
type reducer struct {
	scope  *scope
	walker *ownerWalker
	// gaps accumulate over the life of the reducer. A refused read is not
	// retried once the owner walk has cached its answer, so a Result built
	// later would otherwise stop admitting a hole it still has.
	gaps Gaps
}

func newReducer(s *scope, get objectGetter) *reducer {
	r := &reducer{scope: s}
	r.walker = newOwnerWalker(get, &r.gaps)
	return r
}

// reduce resolves every pod to its top-most owner and keeps the ones the scope
// asked about.
func (r *reducer) reduce(ctx context.Context, pods []*corev1.Pod) ([]owned, error) {
	start := time.Now()

	items := make([]owned, 0, len(pods))
	for _, pod := range pods {
		owner, err := r.walker.TopMost(ctx, pod)
		if err != nil {
			return nil, err
		}
		if !r.scope.targets.wants(pod, owner) {
			continue
		}
		items = append(items, owned{pod: pod, owner: owner})
	}
	klog.V(2).Infof("resolved owners for %d pods in %v", len(pods), time.Since(start))
	return items, nil
}

// result assembles what was reduced, and the failure evidence recorded against
// it, into the Result a run hands back.
func (r *reducer) result(items []owned, events map[types.UID][]corev1.Event) *Result {
	result := &Result{Gaps: r.gaps}
	// The reducer goes on accumulating gaps, so the Result carries a list of
	// its own rather than a window onto one that keeps growing.
	result.Gaps.Owners = slices.Clone(r.gaps.Owners)

	result.Workloads = group(items, events)

	// A workload the user named that has no pods is still worth a row: it is
	// scaled to zero, and its template is the only thing there is to report.
	result.Workloads = append(result.Workloads, r.scope.targets.withoutPods(result.Workloads)...)
	sortWorkloads(result.Workloads)
	return result
}
