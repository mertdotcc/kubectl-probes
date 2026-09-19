// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package model

import (
	"encoding/json"
	"fmt"
	"time"
)

// Duration is a time.Duration that encodes as the string Go prints, such as
// "1m30s". Probe timing is measured in seconds and read by people, so the unit
// belongs in the output rather than in a comment next to a bare number.
type Duration time.Duration

// Seconds returns the Duration a probe field of n seconds describes.
func Seconds[T ~int32 | ~int64 | ~int](n T) Duration {
	return Duration(time.Duration(n) * time.Second)
}

// SecondsPtr is Seconds for the timing fields that are absent when they do not
// apply to a probe.
func SecondsPtr[T ~int32 | ~int64 | ~int](n T) *Duration {
	d := Seconds(n)
	return &d
}

// Duration returns the underlying time.Duration.
func (d Duration) Duration() time.Duration { return time.Duration(d) }

func (d Duration) String() string { return time.Duration(d).String() }

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("duration must be a string such as \"1m30s\": %w", err)
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(parsed)
	return nil
}
