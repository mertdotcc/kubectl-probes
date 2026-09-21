// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package analyze

import (
	"fmt"
	"testing"
	"time"

	"github.com/mertdotcc/kubectl-probes/internal/model"
)

// probeFor is a probe that takes the given number of seconds to notice a
// failure, which is all the Summary reads of one.
func probeFor(seconds int) *model.ProbeDetail {
	return &model.ProbeDetail{
		Config: &model.Probe{Handler: model.Handler{Type: model.HandlerHTTP}},
		Timing: &model.Timing{FailureDetection: model.Seconds(seconds)},
	}
}

// readinessWorkloads is one Deployment per duration, each with a single
// container whose readiness probe takes that many seconds, named so that
// their alphabetical order is the order given.
func readinessWorkloads(seconds ...int) []model.Workload {
	var workloads []model.Workload
	for i, s := range seconds {
		name := fmt.Sprintf("w%03d", i)
		workloads = append(workloads, model.Workload{
			Kind: "Deployment", Group: "apps", Name: name, Namespace: "ns",
			DisplayName: "deploy/" + name,
			Containers:  []model.Container{{Name: "c", Readiness: probeFor(s)}},
		})
	}
	return workloads
}

func summarize(workloads []model.Workload) *model.Summary {
	return Summarize(&model.Report{Workloads: workloads})
}

// Nearest rank puts the P-th percentile at rank ceil(P/100 × n), and never
// below the first, so every percentile names a container that exists.
func TestSummaryNearestRank(t *testing.T) {
	tests := []struct {
		n int
		// ranks are the 1-based ranks of P100, P99, P90, P50, P10 and P0.
		ranks []int
	}{
		{n: 1, ranks: []int{1, 1, 1, 1, 1, 1}},
		{n: 2, ranks: []int{2, 2, 2, 1, 1, 1}},
		{n: 10, ranks: []int{10, 10, 9, 5, 1, 1}},
		{n: 101, ranks: []int{101, 100, 91, 51, 11, 1}},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprint(tt.n), func(t *testing.T) {
			// Container i takes i+1 seconds, so the rank is the duration.
			seconds := make([]int, tt.n)
			for i := range seconds {
				seconds[i] = i + 1
			}
			dist := summarize(readinessWorkloads(seconds...)).FailureDetection.Readiness
			if dist == nil {
				t.Fatal("no readiness distribution")
			}
			if dist.Containers != tt.n {
				t.Errorf("containers = %d, want %d", dist.Containers, tt.n)
			}

			wantPercentiles := []int{100, 99, 90, 50, 10, 0}
			if len(dist.Percentiles) != len(wantPercentiles) {
				t.Fatalf("got %d percentiles, want %d", len(dist.Percentiles), len(wantPercentiles))
			}
			for i, p := range dist.Percentiles {
				if p.Percentile != wantPercentiles[i] {
					t.Errorf("percentile %d is P%d, want P%d", i, p.Percentile, wantPercentiles[i])
				}
				if got, want := p.FailureDetection, model.Seconds(tt.ranks[i]); got != want {
					t.Errorf("P%d = %s, want %s", p.Percentile, got, want)
				}
				if want := fmt.Sprintf("deploy/w%03d", tt.ranks[i]-1); p.Workload != want || p.Namespace != "ns" || p.Container != "c" {
					t.Errorf("P%d names %s %s %s, want ns %s c", p.Percentile, p.Namespace, p.Workload, p.Container, want)
				}
			}
		})
	}
}

// Each bucket's bound is inclusive, so a duration exactly on one falls into
// that bucket and not the next.
func TestSummaryBucketBoundaries(t *testing.T) {
	dist := summarize(readinessWorkloads(1, 10, 11, 30, 31, 60, 61, 120, 121, 300, 301, 3600)).FailureDetection.Readiness

	wantBounds := []time.Duration{10 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute, 0}
	if len(dist.Buckets) != len(wantBounds) {
		t.Fatalf("got %d buckets, want %d", len(dist.Buckets), len(wantBounds))
	}
	for i, b := range dist.Buckets {
		if wantBounds[i] == 0 {
			if b.UpTo != nil {
				t.Errorf("the last bucket is bounded at %s, want it open", b.UpTo)
			}
		} else if b.UpTo == nil || b.UpTo.Duration() != wantBounds[i] {
			t.Errorf("bucket %d is bounded at %v, want %s", i, b.UpTo, wantBounds[i])
		}
		if b.Count != 2 {
			t.Errorf("bucket %d holds %d, want 2", i, b.Count)
		}
	}
}

// A Job or CronJob container is left out of readiness coverage, and out of
// the readiness distribution that coverage counts, because nothing sends its
// pods traffic. It counts everywhere else.
func TestSummaryBatchLeftOutOfReadinessOnly(t *testing.T) {
	workloads := []model.Workload{
		{
			Kind: "Deployment", Group: "apps", Name: "web", Namespace: "ns", DisplayName: "deploy/web",
			Containers: []model.Container{
				{Name: "web", Readiness: probeFor(30), Liveness: probeFor(30)},
				// A sidecar is a row of the Overview and counts like one.
				{Name: "proxy", Sidecar: true, Startup: probeFor(60)},
			},
		},
		{
			Kind: "Job", Group: "batch", Name: "migrate", Namespace: "ns", DisplayName: "job/migrate",
			Containers: []model.Container{{Name: "migrate", Readiness: probeFor(10), Liveness: probeFor(10)}},
		},
		{
			Kind: "CronJob", Group: "batch", Name: "nightly", Namespace: "ns", DisplayName: "cj/nightly",
			Containers: []model.Container{{Name: "report"}},
		},
		// No pods: the template is all there is, and it still counts.
		{
			Kind: "StatefulSet", Group: "apps", Name: "cache", Namespace: "ns", DisplayName: "sts/cache",
			Containers: []model.Container{{Name: "cache"}},
		},
	}

	s := summarize(workloads)
	if s.Containers != 5 || s.Workloads != 4 {
		t.Errorf("counted %d containers in %d workloads, want 5 in 4", s.Containers, s.Workloads)
	}
	want := model.Coverage{
		Startup:           model.Share{Count: 1, Of: 5},
		Readiness:         model.Share{Count: 1, Of: 3},
		Liveness:          model.Share{Count: 2, Of: 5},
		None:              model.Share{Count: 2, Of: 5},
		ReadinessExcluded: 2,
	}
	if s.Coverage != want {
		t.Errorf("coverage = %+v, want %+v", s.Coverage, want)
	}
	if got := s.FailureDetection.Readiness.Containers; got != 1 {
		t.Errorf("the readiness distribution counts %d containers, want 1: the Job's is not counted", got)
	}
	if got := s.FailureDetection.Liveness.Containers; got != 2 {
		t.Errorf("the liveness distribution counts %d containers, want 2: the Job's is counted", got)
	}
}

// A probe the template configures and the running pod does not has no timing
// of its own. The table prints it as a dash, and the Summary does not count it.
func TestSummaryDriftWithoutConfigIsNotCoverage(t *testing.T) {
	s := summarize([]model.Workload{{
		Kind: "Deployment", Group: "apps", Name: "api", Namespace: "ns", DisplayName: "deploy/api",
		Containers: []model.Container{{Name: "api", Readiness: &model.ProbeDetail{Drifted: true}}},
	}})
	if s.Coverage.Readiness.Count != 0 || s.Coverage.None.Count != 1 {
		t.Errorf("coverage = %+v, want no readiness and one container with none", s.Coverage)
	}
	if s.FailureDetection.Readiness != nil {
		t.Errorf("a readiness distribution of %+v, want none", s.FailureDetection.Readiness)
	}
}

// Equal durations are ordered by namespace, workload, then container, so the
// container a percentile names is the same on every run whatever order the
// workloads arrived in.
func TestSummaryTieBreaking(t *testing.T) {
	workload := func(namespace, name string, containers ...string) model.Workload {
		w := model.Workload{Kind: "Deployment", Group: "apps", Name: name, Namespace: namespace, DisplayName: "deploy/" + name}
		for _, c := range containers {
			w.Containers = append(w.Containers, model.Container{Name: c, Liveness: probeFor(30)})
		}
		return w
	}
	forward := []model.Workload{
		workload("a", "zeta", "only"),
		workload("b", "alpha", "x", "y"),
		workload("b", "beta", "a"),
	}
	backward := []model.Workload{forward[2], forward[1], forward[0]}

	for _, workloads := range [][]model.Workload{forward, backward} {
		dist := summarize(workloads).FailureDetection.Liveness
		first, last := dist.Percentiles[len(dist.Percentiles)-1], dist.Percentiles[0]
		if first.Namespace != "a" || first.Workload != "deploy/zeta" || first.Container != "only" {
			t.Errorf("P0 = %+v, want a deploy/zeta only", first)
		}
		if last.Namespace != "b" || last.Workload != "deploy/beta" || last.Container != "a" {
			t.Errorf("P100 = %+v, want b deploy/beta a", last)
		}
		// P50 of four is the second: b deploy/alpha x, ahead of its y.
		if mid := dist.Percentiles[3]; mid.Workload != "deploy/alpha" || mid.Container != "x" {
			t.Errorf("P50 = %+v, want b deploy/alpha x", mid)
		}
	}
}

// A probe no container has gets its coverage line and nothing else, and a
// Report with no containers at all still gets a Summary that says so.
func TestSummaryWithoutProbes(t *testing.T) {
	s := summarize([]model.Workload{{
		Kind: "Pod", Name: "debug", Namespace: "ns", DisplayName: "pod/debug",
		Containers: []model.Container{{Name: "debug"}},
	}})
	if s.FailureDetection.Readiness != nil || s.FailureDetection.Liveness != nil {
		t.Errorf("failure detection = %+v, want none", s.FailureDetection)
	}
	if s.Coverage.None != (model.Share{Count: 1, Of: 1}) {
		t.Errorf("none = %+v, want 1 of 1", s.Coverage.None)
	}

	empty := summarize(nil)
	if empty == nil || empty.Containers != 0 || empty.Workloads != 0 {
		t.Errorf("an empty report summarizes to %+v, want zero containers", empty)
	}
}
