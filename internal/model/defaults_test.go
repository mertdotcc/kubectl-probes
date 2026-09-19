// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package model

import (
	"testing"

	"k8s.io/utils/ptr"
)

// Effective is what the Inspection prints for a field the spec left unset: the
// kubelet's own value, said to be the kubelet's.
func TestEffective(t *testing.T) {
	tests := []struct {
		name      string
		written   *int32
		fallback  int32
		want      int32
		isDefault bool
	}{
		{name: "a field the spec wrote", written: ptr.To(int32(30)), fallback: DefaultFailureThreshold, want: 30},
		{name: "a field the spec left unset", fallback: DefaultFailureThreshold, want: 3, isDefault: true},
		{
			name: "a field written with the value the kubelet would have used",
			// The spec said it, so it is not reported as a default, which is
			// the whole reason the field is a pointer.
			written: ptr.To(int32(10)), fallback: DefaultPeriodSeconds, want: 10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, isDefault := Effective(tt.written, tt.fallback)
			if value != tt.want || isDefault != tt.isDefault {
				t.Errorf("Effective() = %d, %t, want %d, %t", value, isDefault, tt.want, tt.isDefault)
			}
		})
	}
}
