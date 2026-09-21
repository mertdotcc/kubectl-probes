// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package analyze

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/mertdotcc/kubectl-probes/internal/model"
)

// percentiles are the ones kubectl node-resource reports, highest first, which
// is the order the Summary prints them in.
var percentiles = []int{100, 99, 90, 50, 10, 0}

// bucketBounds are the inclusive upper bounds of the failure detection
// histogram, and a final bucket holds everything longer. They are fixed rather
// than drawn from the data, so two clusters' Summaries can be compared by eye.
var bucketBounds = []time.Duration{
	10 * time.Second,
	30 * time.Second,
	time.Minute,
	2 * time.Minute,
	5 * time.Minute,
}

// Summarize counts the Summary from a finished Report.
//
// It reads the Workloads the Overview prints and nothing else, so it can never
// disagree with the table: its unit is a Container, one Overview row, sidecars
// and the containers of workloads with no pods included. It computes no timing
// of its own; failure detection is the figure the Overview's readiness and
// liveness cells already show.
func Summarize(report *model.Report) *model.Summary {
	s := &model.Summary{}
	var readiness, liveness []ranked

	for _, workload := range report.Workloads {
		if len(workload.Containers) == 0 {
			continue
		}
		s.Workloads++
		batch := isBatch(schema.GroupKind{Group: workload.Group, Kind: workload.Kind})

		for _, container := range workload.Containers {
			s.Containers++
			startup, ready, live := timingOfProbe(container.Startup), timingOfProbe(container.Readiness), timingOfProbe(container.Liveness)

			count(&s.Coverage.Startup, startup != nil)
			count(&s.Coverage.Liveness, live != nil)
			count(&s.Coverage.None, startup == nil && ready == nil && live == nil)
			// Nothing sends a Job's pods traffic, which is why
			// no-readiness-probe skips them, and a readiness figure for one
			// would be counted against nothing.
			if batch {
				s.Coverage.ReadinessExcluded++
			} else {
				count(&s.Coverage.Readiness, ready != nil)
			}

			at := func(timing *model.Timing) ranked {
				return ranked{
					duration:  timing.FailureDetection,
					namespace: workload.Namespace,
					workload:  workload.DisplayName,
					container: container.Name,
				}
			}
			if ready != nil && !batch {
				readiness = append(readiness, at(ready))
			}
			if live != nil {
				liveness = append(liveness, at(live))
			}
		}
	}

	s.FailureDetection.Readiness = distributionOf(readiness)
	s.FailureDetection.Liveness = distributionOf(liveness)
	return s
}

// timingOfProbe is a probe's timing when the running configuration has the
// probe. A probe only the template configures is drift, which the table prints
// as a dash, so it is not coverage either.
func timingOfProbe(detail *model.ProbeDetail) *model.Timing {
	if detail == nil || detail.Config == nil {
		return nil
	}
	return detail.Timing
}

func count(share *model.Share, has bool) {
	share.Of++
	if has {
		share.Count++
	}
}

// ranked is one container's failure detection, with what names it.
type ranked struct {
	duration  model.Duration
	namespace string
	workload  string
	container string
}

// compare orders by duration, then by namespace, workload and container, so
// equal durations come out the same on every run.
func (r ranked) compare(other ranked) int {
	return cmp.Or(
		cmp.Compare(r.duration, other.duration),
		strings.Compare(r.namespace, other.namespace),
		strings.Compare(r.workload, other.workload),
		strings.Compare(r.container, other.container),
	)
}

// distributionOf is nil for a probe no container has, which gets its coverage
// line and nothing else.
func distributionOf(values []ranked) *model.Distribution {
	if len(values) == 0 {
		return nil
	}
	slices.SortFunc(values, ranked.compare)

	dist := &model.Distribution{Containers: len(values)}
	for _, p := range percentiles {
		at := values[nearestRank(p, len(values))-1]
		dist.Percentiles = append(dist.Percentiles, model.Percentile{
			Percentile:       p,
			FailureDetection: at.duration,
			Namespace:        at.namespace,
			Workload:         at.workload,
			Container:        at.container,
		})
	}

	for _, bound := range bucketBounds {
		dist.Buckets = append(dist.Buckets, model.Bucket{UpTo: model.SecondsPtr(int64(bound / time.Second))})
	}
	dist.Buckets = append(dist.Buckets, model.Bucket{})
	for _, v := range values {
		i, _ := slices.BinarySearch(bucketBounds, v.duration.Duration())
		dist.Buckets[i].Count++
	}
	return dist
}

// nearestRank is the 1-based rank of the p-th percentile of n values:
// ceil(p/100 × n), and never below the first, so P0 is the minimum and every
// percentile names a value that exists.
func nearestRank(p, n int) int {
	return max((p*n+99)/100, 1)
}
