// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package model

// The kubelet's defaults for the probe fields a spec can leave unset.
//
// A probe read from the API server has had these filled in already, and one
// read from a manifest has not, so a Probe cannot tell a field left unset from
// one written with the default value. It does not need to: both mean the
// kubelet does the same thing.
//
// They live in the model rather than in the package that applies them because
// two packages need the same answer: analyze derives timing and drift from the
// effective configuration, and the Inspection marks which of the values it
// prints the spec never asked for.
const (
	DefaultInitialDelaySeconds int32 = 0
	DefaultPeriodSeconds       int32 = 10
	DefaultTimeoutSeconds      int32 = 1
	DefaultSuccessThreshold    int32 = 1
	DefaultFailureThreshold    int32 = 3
)

// Effective is a probe field's value as the kubelet uses it, and whether that
// value is the kubelet's default rather than something the spec asked for.
func Effective(written *int32, fallback int32) (value int32, isDefault bool) {
	if written == nil {
		return fallback, true
	}
	return *written, false
}
