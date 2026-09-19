// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package analyze

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/mertdotcc/kubectl-probes/internal/collect"
	"github.com/mertdotcc/kubectl-probes/internal/model"
)

// at is a timestamp a fixture can point at, so a test can say "later" without
// depending on when it runs.
func at(minute int) metav1.Time {
	return metav1.NewTime(time.Date(2026, 9, 19, 14, minute, 0, 0, time.UTC))
}

// unhealthyEvent is the event the kubelet records when a probe fails, naming the
// container the way the kubelet names it.
func unhealthyEvent(container, message string, count int32, first, last metav1.Time) corev1.Event {
	return corev1.Event{
		Reason:         "Unhealthy",
		Message:        message,
		Count:          count,
		FirstTimestamp: first,
		LastTimestamp:  last,
		InvolvedObject: corev1.ObjectReference{
			Kind:      "Pod",
			FieldPath: "spec.containers{" + container + "}",
		},
	}
}

func TestPodState(t *testing.T) {
	pod := collect.Pod{
		Pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "api-0", Namespace: "prod"},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "api"}},
			},
			Status: corev1.PodStatus{
				Conditions: []corev1.PodCondition{
					{Type: corev1.ContainersReady, Status: corev1.ConditionFalse, LastTransitionTime: at(22)},
					{Type: corev1.PodScheduled, Status: corev1.ConditionTrue, LastTransitionTime: at(1)},
					{Type: corev1.PodReady, Status: corev1.ConditionFalse, LastTransitionTime: at(22)},
				},
				ContainerStatuses: []corev1.ContainerStatus{{
					Name:         "api",
					Ready:        false,
					Started:      ptr.To(true),
					RestartCount: 4,
					LastTerminationState: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							Reason:     "Error",
							ExitCode:   137,
							Signal:     9,
							FinishedAt: at(21),
						},
					},
				}},
			},
		},
		Events: []corev1.Event{
			unhealthyEvent("api", "Liveness probe failed: HTTP probe failed with statuscode: 503", 7, at(2), at(29)),
			unhealthyEvent("api", "Readiness probe failed: connection refused", 2, at(3), at(28)),
			// Another container's failures are not this container's.
			unhealthyEvent("istio-proxy", "Readiness probe failed: connection refused", 5, at(4), at(5)),
		},
	}

	state, failures := podState(pod, "api")

	if state.Name != "api-0" {
		t.Errorf("Name = %q, want api-0", state.Name)
	}
	if state.Ready {
		t.Error("Ready = true, want false")
	}
	if state.Started == nil || !*state.Started {
		t.Errorf("Started = %v, want true", state.Started)
	}
	if state.Restarts != 4 {
		t.Errorf("Restarts = %d, want 4", state.Restarts)
	}
	if state.LastTermination == nil {
		t.Fatal("LastTermination is absent, want the last termination reported")
	}
	if state.LastTermination.Reason != "Error" || state.LastTermination.ExitCode != 137 || state.LastTermination.Signal != 9 {
		t.Errorf("LastTermination = %+v, want Error, 137, signal 9", *state.LastTermination)
	}
	if state.LastTermination.FinishedAt == nil || !state.LastTermination.FinishedAt.Equal(at(21).Time) {
		t.Errorf("FinishedAt = %v, want %v", state.LastTermination.FinishedAt, at(21).Time)
	}

	// Only the two conditions a probe moves, Ready first.
	wantConditions := []model.Condition{
		{Type: model.ConditionReady, Status: model.ConditionFalse},
		{Type: model.ConditionContainersReady, Status: model.ConditionFalse},
	}
	if len(state.Conditions) != len(wantConditions) {
		t.Fatalf("Conditions = %+v, want Ready and ContainersReady", state.Conditions)
	}
	for i, want := range wantConditions {
		got := state.Conditions[i]
		if got.Type != want.Type || got.Status != want.Status {
			t.Errorf("Conditions[%d] = %+v, want %+v", i, got, want)
		}
		if got.LastTransitionTime == nil || !got.LastTransitionTime.Equal(at(22).Time) {
			t.Errorf("Conditions[%d].LastTransitionTime = %v, want %v", i, got.LastTransitionTime, at(22).Time)
		}
	}

	if failures != 9 {
		t.Errorf("failures = %d, want 9: this container's events only", failures)
	}
}

func TestPodStateWithoutAStatusYet(t *testing.T) {
	pod := collect.Pod{Pod: &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api-0"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "api"}}},
	}}

	state, failures := podState(pod, "api")
	if state.Name != "api-0" || state.Ready || state.Started != nil || state.Restarts != 0 {
		t.Errorf("state = %+v, want a pod the kubelet has not reported on yet", state)
	}
	if state.LastTermination != nil || state.Events != nil || failures != 0 {
		t.Errorf("state = %+v, failures = %d, want no evidence at all", state, failures)
	}
}

func TestPodStateFindsASidecarStatus(t *testing.T) {
	pod := collect.Pod{Pod: &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api-0"},
		Spec: corev1.PodSpec{
			InitContainers: []corev1.Container{{
				Name:          "istio-proxy",
				RestartPolicy: ptr.To(corev1.ContainerRestartPolicyAlways),
			}},
		},
		Status: corev1.PodStatus{
			InitContainerStatuses: []corev1.ContainerStatus{{Name: "istio-proxy", Ready: true, RestartCount: 1}},
		},
	}}

	state, _ := podState(pod, "istio-proxy")
	if !state.Ready || state.Restarts != 1 {
		t.Errorf("state = %+v, want the sidecar's own status", state)
	}
}

func TestGroupEvents(t *testing.T) {
	tests := []struct {
		name   string
		events []corev1.Event
		want   []model.EventGroup
	}{
		{
			name: "one probe, one event",
			events: []corev1.Event{
				unhealthyEvent("api", "Liveness probe failed: 503", 7, at(2), at(29)),
			},
			want: []model.EventGroup{{
				Probe:     model.ProbeLiveness,
				Count:     7,
				FirstSeen: timeOf(at(2)),
				LastSeen:  timeOf(at(29)),
				Message:   "Liveness probe failed: 503",
			}},
		},
		{
			name: "every probe, in the order the kubelet puts them to work",
			events: []corev1.Event{
				unhealthyEvent("api", "Liveness probe failed: 503", 1, at(20), at(20)),
				unhealthyEvent("api", "Startup probe failed: connection refused", 3, at(1), at(3)),
				unhealthyEvent("api", "Readiness probe errored: rpc error: code = Unknown", 2, at(10), at(11)),
			},
			want: []model.EventGroup{
				{
					Probe:     model.ProbeStartup,
					Count:     3,
					FirstSeen: timeOf(at(1)),
					LastSeen:  timeOf(at(3)),
					Message:   "Startup probe failed: connection refused",
				},
				{
					Probe:     model.ProbeReadiness,
					Count:     2,
					FirstSeen: timeOf(at(10)),
					LastSeen:  timeOf(at(11)),
					Message:   "Readiness probe errored: rpc error: code = Unknown",
				},
				{
					Probe:     model.ProbeLiveness,
					Count:     1,
					FirstSeen: timeOf(at(20)),
					LastSeen:  timeOf(at(20)),
					Message:   "Liveness probe failed: 503",
				},
			},
		},
		{
			name: "several events for one probe collapse into its span",
			events: []corev1.Event{
				unhealthyEvent("api", "Liveness probe failed: 503", 4, at(10), at(14)),
				unhealthyEvent("api", "Liveness probe failed: connection refused", 2, at(2), at(4)),
			},
			want: []model.EventGroup{{
				Probe:     model.ProbeLiveness,
				Count:     6,
				FirstSeen: timeOf(at(2)),
				LastSeen:  timeOf(at(14)),
				// The latest message, not the last one the API server listed.
				Message: "Liveness probe failed: 503",
			}},
		},
		{
			name: "an event the API server never aggregated happened once",
			events: []corev1.Event{
				unhealthyEvent("api", "Liveness probe failed: 503", 0, at(10), at(10)),
			},
			want: []model.EventGroup{{
				Probe:     model.ProbeLiveness,
				Count:     1,
				FirstSeen: timeOf(at(10)),
				LastSeen:  timeOf(at(10)),
				Message:   "Liveness probe failed: 503",
			}},
		},
		{
			name: "a message that names no probe belongs to no group",
			events: []corev1.Event{
				unhealthyEvent("api", "Something else entirely", 3, at(10), at(11)),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			groups := groupEvents(tt.events)
			if len(groups) != len(tt.want) {
				t.Fatalf("groups = %+v, want %d group(s)", groups, len(tt.want))
			}
			for i, want := range tt.want {
				got := groups[i]
				if got.Probe != want.Probe || got.Count != want.Count || got.Message != want.Message {
					t.Errorf("groups[%d] = %+v, want %+v", i, got, want)
				}
				if got.FirstSeen == nil || !got.FirstSeen.Equal(*want.FirstSeen) {
					t.Errorf("groups[%d].FirstSeen = %v, want %v", i, got.FirstSeen, want.FirstSeen)
				}
				if got.LastSeen == nil || !got.LastSeen.Equal(*want.LastSeen) {
					t.Errorf("groups[%d].LastSeen = %v, want %v", i, got.LastSeen, want.LastSeen)
				}
			}
		})
	}
}

// An event written through the events API carries its count and its times
// somewhere else entirely, and reaches this tool in the same list.
func TestGroupEventsFromASeries(t *testing.T) {
	event := corev1.Event{
		Reason:    "Unhealthy",
		Message:   "Readiness probe failed: 503",
		EventTime: metav1.NewMicroTime(at(5).Time),
		Series: &corev1.EventSeries{
			Count:            12,
			LastObservedTime: metav1.NewMicroTime(at(19).Time),
		},
		InvolvedObject: corev1.ObjectReference{Kind: "Pod", FieldPath: "spec.containers{api}"},
	}

	groups := groupEvents([]corev1.Event{event})
	if len(groups) != 1 {
		t.Fatalf("groups = %+v, want one", groups)
	}
	group := groups[0]
	if group.Count != 12 {
		t.Errorf("Count = %d, want 12", group.Count)
	}
	if group.FirstSeen == nil || !group.FirstSeen.Equal(at(5).Time) {
		t.Errorf("FirstSeen = %v, want %v", group.FirstSeen, at(5).Time)
	}
	if group.LastSeen == nil || !group.LastSeen.Equal(at(19).Time) {
		t.Errorf("LastSeen = %v, want %v", group.LastSeen, at(19).Time)
	}
}

func TestContainerOfEvent(t *testing.T) {
	tests := []struct {
		fieldPath string
		want      string
		wantOK    bool
	}{
		{fieldPath: "spec.containers{api}", want: "api", wantOK: true},
		{fieldPath: "spec.initContainers{istio-proxy}", want: "istio-proxy", wantOK: true},
		{fieldPath: "spec.containers{api}.image", want: "api", wantOK: true},
		{fieldPath: ""},
		{fieldPath: "spec.containers"},
	}

	for _, tt := range tests {
		t.Run(tt.fieldPath, func(t *testing.T) {
			got, ok := containerOfEvent(corev1.Event{
				InvolvedObject: corev1.ObjectReference{FieldPath: tt.fieldPath},
			})
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("containerOfEvent(%q) = %q, %v, want %q, %v", tt.fieldPath, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}
