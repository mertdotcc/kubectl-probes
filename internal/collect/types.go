// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package collect

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Result is everything one run read, exactly as the API server returned it.
// Nothing here is interpreted; that is the analyze package's work.
type Result struct {
	Workloads []Workload
	Gaps      Gaps
}

// Workload is a top-most owner and the pods that belong to it. A workload with
// no pods is still a workload: scaled to zero, or read from a manifest.
type Workload struct {
	GroupKind schema.GroupKind
	Name      string
	Namespace string
	// Owner is the top-most owner object. It is nil for a bare pod, which
	// owns itself, and for an owner the user may not read.
	Owner *unstructured.Unstructured
	// Template is the pod template carried by Owner, or a bare pod's own spec.
	// It is nil when the kind has none, when the owner could not be read, or
	// when a custom kind keeps its template somewhere this tool does not look.
	Template *corev1.PodSpec
	Pods     []Pod
}

// Pod is one pod and the failure evidence the cluster reports for it.
type Pod struct {
	Pod *corev1.Pod
	// Events are the pod's Unhealthy events, in the order the API server
	// returned them.
	Events []corev1.Event
}

// Gaps is what the user was not allowed to read. A gap is never fatal: the run
// reports what it could see and says what it could not, because a report that
// silently omits evidence is worse than one that admits the hole.
type Gaps struct {
	// Events is set when listing events was forbidden in any namespace, which
	// makes failure counts unknown rather than zero.
	Events bool
	// Owners are the kinds whose objects could not be read. Pods owned by one
	// are reported under that owner with no template, so drift is unknown.
	Owners []schema.GroupKind
}

// forbidOwner records an owner kind the user may not read, once.
func (g *Gaps) forbidOwner(gk schema.GroupKind) {
	for _, known := range g.Owners {
		if known == gk {
			return
		}
	}
	g.Owners = append(g.Owners, gk)
}
