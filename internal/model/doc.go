// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

// Package model holds the versioned Report schema shared by collect, analyze,
// and render: workloads, containers, probes, effective timing, drift, runtime
// state, failure evidence, and findings.
//
// The schema carries apiVersion and kind so consumers can detect changes, and
// starts at v1alpha1.
package model
