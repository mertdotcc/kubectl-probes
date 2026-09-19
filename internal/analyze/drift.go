// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package analyze

import (
	"reflect"

	corev1 "k8s.io/api/core/v1"
)

// drifted reports whether a running pod's probe and its workload template's
// probe would have the kubelet behave differently.
//
// The comparison is of behaviour, not of wording: defaults are applied to both
// sides first, so a template that writes periodSeconds: 10 and a pod that
// leaves it unset agree. A probe configured on one side only is drift, which
// covers both a rollout that added a probe and one that removed it.
func drifted(running, template *corev1.Probe) bool {
	return !reflect.DeepEqual(effective(running), effective(template))
}
