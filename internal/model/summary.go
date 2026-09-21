// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package model

// Summary is the closing section of the Overview: probe coverage and the
// distribution of failure detection across the Containers the Overview covers.
// It is counted from the Report's own Workloads, so it can never say something
// the table does not, and it carries facts only, never findings.
type Summary struct {
	// Containers counts the Overview's rows, sidecars and the containers of
	// workloads with no pods included.
	Containers int `json:"containers"`
	// Workloads counts the workloads those containers belong to.
	Workloads        int              `json:"workloads"`
	Coverage         Coverage         `json:"coverage"`
	FailureDetection FailureDetection `json:"failureDetection"`
}

// Coverage is how many of the containers have each kind of probe, and how many
// have none at all.
type Coverage struct {
	Startup   Share `json:"startup"`
	Readiness Share `json:"readiness"`
	Liveness  Share `json:"liveness"`
	None      Share `json:"none"`
	// ReadinessExcluded counts the Job and CronJob containers left out of
	// Readiness: nothing sends their pods traffic, so whether they have a
	// readiness probe says nothing. They count everywhere else.
	ReadinessExcluded int `json:"readinessExcluded,omitempty"`
}

// Share is a count out of a total.
type Share struct {
	Count int `json:"count"`
	Of    int `json:"of"`
}

// FailureDetection is how long a failing container goes unnoticed, for the two
// probes that watch a container once it is up. Each is absent when no
// container in scope has that probe.
type FailureDetection struct {
	Readiness *Distribution `json:"readiness,omitempty"`
	Liveness  *Distribution `json:"liveness,omitempty"`
}

// Distribution is the failure detection of one probe across the containers
// that have it: the containers at a fixed set of percentiles, and how many fall
// into each of a fixed set of buckets.
type Distribution struct {
	Containers  int          `json:"containers"`
	Percentiles []Percentile `json:"percentiles"`
	Buckets     []Bucket     `json:"buckets"`
}

// Percentile is the container at one nearest-rank percentile, named so that
// an outlier is one command away from its Inspection.
type Percentile struct {
	Percentile       int      `json:"percentile"`
	FailureDetection Duration `json:"failureDetection"`
	Namespace        string   `json:"namespace,omitempty"`
	// Workload is the workload's display name, as the Overview prints it.
	Workload  string `json:"workload"`
	Container string `json:"container"`
}

// Bucket counts the containers whose failure detection is at most UpTo and
// more than the bucket before it. The last bucket has no UpTo and holds
// everything longer than the one before it.
type Bucket struct {
	UpTo  *Duration `json:"upTo,omitempty"`
	Count int       `json:"count"`
}
