// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package collect

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func event(reason, kind, name string, uid types.UID, message string) corev1.Event {
	return corev1.Event{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: name + ".1"},
		Reason:     reason,
		Message:    message,
		InvolvedObject: corev1.ObjectReference{
			Kind:      kind,
			Namespace: "prod",
			Name:      name,
			UID:       uid,
		},
	}
}

func TestEventsByPod(t *testing.T) {
	events := []corev1.Event{
		event("Unhealthy", "Pod", "api-0", "uid-api-0", "Liveness probe failed: HTTP probe failed with statuscode: 503"),
		event("Unhealthy", "Pod", "api-0", "uid-api-0", "Readiness probe failed: connection refused"),
		event("Unhealthy", "Pod", "api-1", "uid-api-1", "Startup probe failed: dial tcp: connect: connection refused"),
		// Not probe failures.
		event("Killing", "Pod", "api-0", "uid-api-0", "Stopping container api"),
		event("BackOff", "Pod", "api-1", "uid-api-1", "Back-off restarting failed container"),
		// Not about a pod.
		event("Unhealthy", "Deployment", "api", "uid-deploy", "something else"),
		// Old enough that the API server no longer carries the UID.
		event("Unhealthy", "Pod", "api-2", "", "Liveness probe failed"),
	}

	byPod := eventsByPod(events)

	if len(byPod) != 2 {
		t.Fatalf("eventsByPod returned %d pods, want 2", len(byPod))
	}
	if got := len(byPod["uid-api-0"]); got != 2 {
		t.Errorf("api-0 has %d events, want 2", got)
	}
	if got := len(byPod["uid-api-1"]); got != 1 {
		t.Errorf("api-1 has %d events, want 1", got)
	}
	if _, ok := byPod[""]; ok {
		t.Error("an event with no involved UID was kept")
	}
}

// A StatefulSet replaces db-0 with a new db-0, and the failures of the pod
// that is gone are not the failures of the pod that replaced it.
func TestEventsByPodSeparatesReusedNames(t *testing.T) {
	events := []corev1.Event{
		event("Unhealthy", "Pod", "db-0", "uid-old", "Liveness probe failed: old"),
		event("Unhealthy", "Pod", "db-0", "uid-new", "Liveness probe failed: new"),
	}

	byPod := eventsByPod(events)

	if len(byPod) != 2 {
		t.Fatalf("eventsByPod returned %d pods, want the two db-0 pods kept apart", len(byPod))
	}
	if byPod["uid-new"][0].Message != "Liveness probe failed: new" {
		t.Errorf("the replacement pod got %q", byPod["uid-new"][0].Message)
	}
}
