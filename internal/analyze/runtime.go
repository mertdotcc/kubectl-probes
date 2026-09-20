// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package analyze

import (
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/mertdotcc/kubectl-probes/internal/collect"
	"github.com/mertdotcc/kubectl-probes/internal/model"
)

// probeMessages are the prefixes the kubelet writes an Unhealthy event with,
// which is the only thing in the event that says which probe failed. The word
// after "probe" varies: a check that returns the wrong answer failed, and one
// that could not be made at all errored.
var probeMessages = map[string]model.ProbeType{
	"Startup probe":   model.ProbeStartup,
	"Readiness probe": model.ProbeReadiness,
	"Liveness probe":  model.ProbeLiveness,
}

// podState is what the cluster reports about one container in one pod, and the
// failure evidence recorded against it. The second return is how many Unhealthy
// events the container has, which the workload's failure count sums.
func podState(pod collect.Pod, container string) (model.Pod, int32) {
	state := model.Pod{
		Name: pod.Pod.Name,
		// The node is what the kubelet that runs these probes is called. A pod
		// read from a manifest has none, and neither has one still waiting to
		// be scheduled.
		Node:       pod.Pod.Spec.NodeName,
		Conditions: conditionsOf(pod.Pod),
	}
	if status := containerStatus(pod.Pod, container); status != nil {
		state.Ready = status.Ready
		state.Started = clone(status.Started)
		state.Restarts = status.RestartCount
		state.LastTermination = terminationOf(status)
	}

	events := eventsFor(pod.Events, container)
	state.Events = groupEvents(events)

	var failures int32
	for _, event := range events {
		failures += eventCount(event)
	}
	return state, failures
}

// containerStatus finds the kubelet's status for one container, whether it runs
// as a regular container or as a sidecar. A pod that has only just been
// scheduled has no status for it at all.
func containerStatus(pod *corev1.Pod, container string) *corev1.ContainerStatus {
	for _, statuses := range [][]corev1.ContainerStatus{pod.Status.ContainerStatuses, pod.Status.InitContainerStatuses} {
		for i := range statuses {
			if statuses[i].Name == container {
				return &statuses[i]
			}
		}
	}
	return nil
}

// conditionsOf carries the two pod conditions a probe moves, in the order the
// kubelet reasons about them: ContainersReady follows the containers' own
// readiness, and Ready follows it plus the pod's readiness gates.
func conditionsOf(pod *corev1.Pod) []model.Condition {
	var out []model.Condition
	for _, want := range []string{model.ConditionReady, model.ConditionContainersReady} {
		for _, condition := range pod.Status.Conditions {
			if string(condition.Type) != want {
				continue
			}
			out = append(out, model.Condition{
				Type:               want,
				Status:             model.ConditionStatus(condition.Status),
				LastTransitionTime: timeOf(condition.LastTransitionTime),
			})
			break
		}
	}
	return out
}

func terminationOf(status *corev1.ContainerStatus) *model.Termination {
	terminated := status.LastTerminationState.Terminated
	if terminated == nil {
		return nil
	}
	return &model.Termination{
		Reason:     terminated.Reason,
		ExitCode:   terminated.ExitCode,
		Signal:     terminated.Signal,
		FinishedAt: timeOf(terminated.FinishedAt),
	}
}

// eventsFor keeps the events recorded against one container of a pod.
//
// The kubelet names the container in the involved object's field path, as
// spec.containers{api} or spec.initContainers{istio-proxy}. An event that names
// no container is left out rather than guessed at: a failure counted against
// the wrong container is worse than one that is missing.
func eventsFor(events []corev1.Event, container string) []corev1.Event {
	var out []corev1.Event
	for _, event := range events {
		if name, ok := containerOfEvent(event); ok && name == container {
			out = append(out, event)
		}
	}
	return out
}

func containerOfEvent(event corev1.Event) (string, bool) {
	path := event.InvolvedObject.FieldPath
	open := strings.Index(path, "{")
	closing := strings.LastIndex(path, "}")
	if open < 0 || closing < open {
		return "", false
	}
	return path[open+1 : closing], true
}

// groupEvents collapses a container's Unhealthy events into one group per
// probe: how often it failed, over what span, and what it last said.
//
// An event whose message names no probe is counted as a failure but belongs to
// no group, because the report would otherwise claim a probe the cluster did
// not name.
func groupEvents(events []corev1.Event) []model.EventGroup {
	groups := map[model.ProbeType]*model.EventGroup{}
	for _, event := range events {
		probe, ok := probeOfMessage(event.Message)
		if !ok {
			continue
		}
		group, seen := groups[probe]
		if !seen {
			group = &model.EventGroup{Probe: probe}
			groups[probe] = group
		}
		group.Count += eventCount(event)

		first, last := seenAt(event)
		if first != nil && (group.FirstSeen == nil || first.Before(*group.FirstSeen)) {
			group.FirstSeen = first
		}
		// The latest message is the one worth printing: an event series carries
		// only the message of its most recent occurrence anyway.
		if group.LastSeen == nil || (last != nil && !last.Before(*group.LastSeen)) {
			group.Message = event.Message
		}
		if last != nil && (group.LastSeen == nil || last.After(*group.LastSeen)) {
			group.LastSeen = last
		}
	}

	if len(groups) == 0 {
		return nil
	}
	out := make([]model.EventGroup, 0, len(groups))
	for _, probe := range model.ProbeTypes {
		if group, ok := groups[probe]; ok {
			out = append(out, *group)
		}
	}
	return out
}

func probeOfMessage(message string) (model.ProbeType, bool) {
	for prefix, probe := range probeMessages {
		if strings.HasPrefix(message, prefix) {
			return probe, true
		}
	}
	return "", false
}

// eventCount is how many times an event happened. A series carries its own
// count, and an event the API server never aggregated carries none at all,
// which is one occurrence rather than none.
func eventCount(event corev1.Event) int32 {
	if event.Series != nil && event.Series.Count > 0 {
		return event.Series.Count
	}
	if event.Count > 0 {
		return event.Count
	}
	return 1
}

// seenAt is when an event was first and last observed. Old events carry a first
// and last timestamp, events written through the events API carry an event time
// and a series, and either shape can reach this tool.
func seenAt(event corev1.Event) (first, last *time.Time) {
	eventTime := microTimeOf(event.EventTime)

	first = timeOf(event.FirstTimestamp)
	if first == nil {
		first = eventTime
	}

	last = timeOf(event.LastTimestamp)
	if last == nil && event.Series != nil {
		last = microTimeOf(event.Series.LastObservedTime)
	}
	if last == nil {
		last = eventTime
	}
	if last == nil {
		last = first
	}
	return first, last
}

// timeOf reports a timestamp the cluster actually set, in UTC, so that a report
// of the same cluster reads the same wherever it was produced.
func timeOf(t metav1.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	utc := t.Time.UTC()
	return &utc
}

func microTimeOf(t metav1.MicroTime) *time.Time {
	if t.IsZero() {
		return nil
	}
	utc := t.Time.UTC()
	return &utc
}
