// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

// Package analyze turns collected facts into the parts of a Report that need
// interpretation: effective timing derived from a probe's raw fields, drift
// between a pod's spec and its workload's pod template, per-container
// aggregation of runtime state, and the rules that produce findings.
//
// Findings tell a user where to investigate; they never propose a fix.
package analyze
