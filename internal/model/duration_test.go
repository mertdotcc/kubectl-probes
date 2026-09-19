// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package model

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDurationMarshalJSON(t *testing.T) {
	tests := []struct {
		name string
		d    Duration
		want string
	}{
		{name: "zero is still a measurement", d: Seconds(0), want: `"0s"`},
		{name: "seconds", d: Seconds(10), want: `"10s"`},
		{name: "minutes and seconds", d: Seconds(90), want: `"1m30s"`},
		{name: "whole minutes", d: Seconds(300), want: `"5m0s"`},
		{name: "hours", d: Seconds(3661), want: `"1h1m1s"`},
		{name: "int32 field", d: Seconds(int32(45)), want: `"45s"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := json.Marshal(tt.d)
			if err != nil {
				t.Fatalf("Marshal(%v) failed: %v", time.Duration(tt.d), err)
			}
			if string(b) != tt.want {
				t.Errorf("Marshal(%v) = %s, want %s", time.Duration(tt.d), b, tt.want)
			}
		})
	}
}

func TestDurationUnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    Duration
		wantErr bool
	}{
		{name: "seconds", in: `"10s"`, want: Seconds(10)},
		{name: "minutes and seconds", in: `"1m30s"`, want: Seconds(90)},
		{name: "zero", in: `"0s"`, want: 0},
		{name: "a bare number is not a duration", in: `90`, wantErr: true},
		{name: "unparseable", in: `"soon"`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got Duration
			err := json.Unmarshal([]byte(tt.in), &got)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Unmarshal(%s) = %v, want an error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Unmarshal(%s) failed: %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("Unmarshal(%s) = %v, want %v", tt.in, got.Duration(), tt.want.Duration())
			}
		})
	}
}

func TestSecondsPtr(t *testing.T) {
	got := SecondsPtr(int32(5))
	if got == nil || *got != Seconds(5) {
		t.Fatalf("SecondsPtr(5) = %v, want a pointer to 5s", got)
	}
}
