// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package collect

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

// unhealthyReason is the event reason the kubelet uses for every probe
// failure, whichever probe failed.
const unhealthyReason = "Unhealthy"

// eventsByPod keeps the Unhealthy events that concern a pod, keyed by the
// pod's UID.
//
// Matching is by UID rather than by name because a pod name is reused: a
// StatefulSet replaces db-0 with a new db-0, and the old pod's failures are
// not the new pod's failures.
func eventsByPod(events []corev1.Event) map[types.UID][]corev1.Event {
	byPod := map[types.UID][]corev1.Event{}
	for _, event := range events {
		if event.Reason != unhealthyReason {
			continue
		}
		if event.InvolvedObject.Kind != "Pod" || event.InvolvedObject.UID == "" {
			continue
		}
		byPod[event.InvolvedObject.UID] = append(byPod[event.InvolvedObject.UID], event)
	}
	return byPod
}
