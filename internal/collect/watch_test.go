// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package collect

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

// The watch tests drive real informers against a fake clientset, so every
// assertion below is about something arriving rather than something returning.
// These are the windows they wait in: long enough that a loaded machine does
// not fail a test, short enough that a genuine failure is not a coffee break.
const (
	// arrives is how long a Result that should come is waited for.
	arrives = 5 * time.Second
	// stillness is how long the channel is watched to prove that a Result
	// that should not come does not.
	stillness = 400 * time.Millisecond
)

// watchedPod is a pod of deploy/api as the kubelet reports it, with the one
// condition these tests flip.
func watchedPod(name string, uid types.UID, node string, ready bool) *corev1.Pod {
	p := pod(name, uid, controllerRef("apps/v1", "ReplicaSet", "api-7d9f4c8b6d", "rs-uid"))
	p.Spec.NodeName = node
	p.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: readiness(ready)}}
	return p
}

func readiness(ready bool) corev1.ConditionStatus {
	if ready {
		return corev1.ConditionTrue
	}
	return corev1.ConditionFalse
}

// unhealthy is one probe failure as the kubelet records it. The name is given
// rather than derived because a burst of them is several objects, and the API
// server will not hold two of the same name.
func unhealthy(name, podName string, uid types.UID, message string) *corev1.Event {
	return &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Namespace: "prod", Name: name},
		Reason:         unhealthyReason,
		Message:        message,
		InvolvedObject: corev1.ObjectReference{Kind: "Pod", Namespace: "prod", Name: podName, UID: uid},
	}
}

// watchScope is a run that named nothing: every pod in prod, however it is
// labelled.
func watchScope() *scope {
	return &scope{namespace: "prod", targets: &targets{all: true}}
}

// quickly shrinks the coalescing windows, because a test should not wait out a
// quarter of a second of quiet to see what a rolling update settles on.
func quickly(t *testing.T, quiet, limit time.Duration) {
	t.Helper()
	wasQuiet, wasLimit := coalesceQuiet, coalesceCap
	coalesceQuiet, coalesceCap = quiet, limit
	t.Cleanup(func() { coalesceQuiet, coalesceCap = wasQuiet, wasLimit })
}

// receive takes the next Result, and fails the test rather than hanging when
// none comes.
func receive(t *testing.T, results <-chan *Result, what string) *Result {
	t.Helper()
	select {
	case result, ok := <-results:
		if !ok {
			t.Fatalf("the channel closed before %s arrived", what)
		}
		return result
	case <-time.After(arrives):
		t.Fatalf("%s did not arrive within %v", what, arrives)
		return nil
	}
}

// podsOf is the pods of the one workload a Result holds, so an assertion can
// read a pod out of it without walking the whole shape.
func podsOf(t *testing.T, result *Result) []Pod {
	t.Helper()
	if len(result.Workloads) != 1 {
		t.Fatalf("the Result holds %d workloads, want the one Deployment", len(result.Workloads))
	}
	return result.Workloads[0].Pods
}

// Watch and Collect must never disagree about what a workload is, which is
// what sharing the scope and the reduction is for. The first Result is the
// proof: same fixtures, same cluster, same answer.
func TestWatchFirstResultMatchesCollect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := fake.NewClientset(
		watchedPod("api-7d9f4c8b6d-2xk9v", "uid-1", "worker-1", true),
		watchedPod("api-7d9f4c8b6d-9wq4t", "uid-2", "worker-2", false),
		unhealthy("api-7d9f4c8b6d-9wq4t.1", "api-7d9f4c8b6d-9wq4t", "uid-2", "Readiness probe failed: HTTP probe failed with statuscode: 503"),
	)
	f := deploymentFixtures()

	once, err := collectFrom(ctx, client, newReducer(watchScope(), f.get))
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	results, err := watchFrom(ctx, client, newReducer(watchScope(), f.get))
	if err != nil {
		t.Fatalf("Watch failed: %v", err)
	}
	first := receive(t, results, "the first Result")

	if !reflect.DeepEqual(first, once) {
		t.Errorf("Watch's first Result is not what Collect returned:\nwatch:   %+v\ncollect: %+v", first, once)
	}
}

// The reason to watch at all: a pod the kubelet has stopped calling ready is
// a pod the consumer has to be told about.
func TestWatchReEmitsWhenAPodChanges(t *testing.T) {
	quickly(t, 20*time.Millisecond, 100*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := fake.NewClientset(watchedPod("api-7d9f4c8b6d-2xk9v", "uid-1", "worker-1", true))
	results, err := watchFrom(ctx, client, newReducer(watchScope(), deploymentFixtures().get))
	if err != nil {
		t.Fatalf("Watch failed: %v", err)
	}

	first := receive(t, results, "the first Result")
	if got := podsOf(t, first)[0].Pod.Status.Conditions[0].Status; got != corev1.ConditionTrue {
		t.Fatalf("the pod starts out %s, want the ready one", got)
	}

	failing := watchedPod("api-7d9f4c8b6d-2xk9v", "uid-1", "worker-1", false)
	if _, err := client.CoreV1().Pods("prod").Update(ctx, failing, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("updating the pod failed: %v", err)
	}

	second := receive(t, results, "the Result the change should produce")
	pods := podsOf(t, second)
	if len(pods) != 1 {
		t.Fatalf("the second Result holds %d pods, want 1", len(pods))
	}
	if got := pods[0].Pod.Status.Conditions[0].Status; got != corev1.ConditionFalse {
		t.Errorf("the pod is %s in the second Result, want the kubelet's False", got)
	}
}

// A rolling update, or a container failing its probe every few seconds, is a
// burst of changes and one state worth drawing. The consumer gets the state.
func TestWatchCoalescesABurstOfEvents(t *testing.T) {
	quickly(t, 100*time.Millisecond, time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := fake.NewClientset(watchedPod("api-7d9f4c8b6d-2xk9v", "uid-1", "worker-1", false))
	results, err := watchFrom(ctx, client, newReducer(watchScope(), deploymentFixtures().get))
	if err != nil {
		t.Fatalf("Watch failed: %v", err)
	}
	receive(t, results, "the first Result")

	for _, name := range []string{"burst.1", "burst.2", "burst.3"} {
		event := unhealthy(name, "api-7d9f4c8b6d-2xk9v", "uid-1", "Liveness probe failed: connection refused")
		if _, err := client.CoreV1().Events("prod").Create(ctx, event, metav1.CreateOptions{}); err != nil {
			t.Fatalf("recording %s failed: %v", name, err)
		}
	}

	// One Result, carrying all three failures rather than the first of them.
	second := receive(t, results, "the Result the burst should produce")
	if got := len(podsOf(t, second)[0].Events); got != 3 {
		t.Errorf("the coalesced Result carries %d events, want all 3 of the burst", got)
	}

	select {
	case extra, ok := <-results:
		if ok {
			t.Errorf("a burst of three events produced a second Result: %+v", extra)
		}
	case <-time.After(stillness):
	}
}

// The channel is how a consumer learns the watch is over, so it has to close
// and not merely go quiet.
func TestWatchClosesTheChannelWhenTheContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	client := fake.NewClientset(watchedPod("api-7d9f4c8b6d-2xk9v", "uid-1", "worker-1", true))
	results, err := watchFrom(ctx, client, newReducer(watchScope(), deploymentFixtures().get))
	if err != nil {
		t.Fatalf("Watch failed: %v", err)
	}
	receive(t, results, "the first Result")

	cancel()

	deadline := time.After(arrives)
	for {
		select {
		case _, ok := <-results:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatalf("the channel was still open %v after the context ended", arrives)
		}
	}
}

// A consumer redrawing a Dashboard must never hold the watch up, and the
// Result it was too slow to take is a state the cluster has already left.
func TestWatchKeepsOnlyTheNewestResult(t *testing.T) {
	out := make(chan *Result, 1)
	first, second := &Result{}, &Result{}

	emit(out, first)
	emit(out, second)

	if got := <-out; got != second {
		t.Error("the consumer was handed the Result it had already fallen behind")
	}
	select {
	case got := <-out:
		t.Errorf("a queue built up behind the consumer: %+v", got)
	default:
	}
}

// A manifest is a cluster frozen at the moment it was written. There is
// nothing in it to watch, and saying so is better than serving one snapshot
// and never moving again.
func TestWatchRejectsFilenames(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	results, err := Watch(ctx, Options{Filenames: []string{"testdata/api.yaml"}})
	if err == nil {
		t.Fatal("Watch accepted -f, want an error")
	}
	if results != nil {
		t.Error("Watch returned a channel along with the error")
	}
}

// Pods are the report. Being refused them is not a gap to note and survive,
// it is the end of the run.
func TestWatchFailsWhenPodsAreForbidden(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := fake.NewClientset()
	forbid(client, "pods")

	if _, err := watchFrom(ctx, client, newReducer(watchScope(), deploymentFixtures().get)); !apierrors.IsForbidden(err) {
		t.Fatalf("Watch failed with %v, want a Forbidden error", err)
	}
}

// Per ADR-0002 a gap is never fatal: without events the failure counts are
// unknown rather than zero, and the configuration is still worth reporting.
func TestWatchRecordsAGapWhenEventsAreForbidden(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := fake.NewClientset(watchedPod("api-7d9f4c8b6d-2xk9v", "uid-1", "worker-1", true))
	forbid(client, "events")

	results, err := watchFrom(ctx, client, newReducer(watchScope(), deploymentFixtures().get))
	if err != nil {
		t.Fatalf("Watch failed although only events were refused: %v", err)
	}

	first := receive(t, results, "the first Result")
	if !first.Gaps.Events {
		t.Error("events were forbidden and the Result does not admit the gap")
	}
	if len(podsOf(t, first)) != 1 {
		t.Error("the pod is missing from a report that could still describe it")
	}
}

// forbid makes a resource unreadable the way RBAC does, for both the list the
// reflector starts with and the watch it would go on to.
func forbid(client *fake.Clientset, resource string) {
	refuse := func(action ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(
			schema.GroupResource{Resource: resource}, "", errors.New("RBAC: access denied"))
	}
	client.PrependReactor("list", resource, refuse)
	client.PrependWatchReactor(resource, func(action ktesting.Action) (bool, watch.Interface, error) {
		_, _, err := refuse(action)
		return true, nil, err
	})
}
