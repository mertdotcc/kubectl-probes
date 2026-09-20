// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package serve

import (
	"fmt"
	"testing"
	"time"
)

// at is one Report in the ring, labelled with the minute it was published so
// an assertion can name the ones that should have survived.
func at(minute int) entry {
	return entry{
		at:    time.Date(2026, 9, 20, 12, minute, 0, 0, time.UTC),
		frame: []byte(fmt.Sprintf("minute %d", minute)),
	}
}

// The ring is the observed part of the Timeline, and a Timeline that grew
// without end would be a memory leak with a nice name. Two things bound it:
// how far back it reaches, and how much of it there is.
func TestRingDropsWhatItCannotHold(t *testing.T) {
	tests := []struct {
		name   string
		window time.Duration
		limit  int
		added  []entry
		want   []string
	}{
		{
			name:   "an empty ring replays nothing",
			window: time.Hour,
			limit:  10,
			want:   nil,
		},
		{
			name:   "everything inside the window is replayed, oldest first",
			window: time.Hour,
			limit:  10,
			added:  []entry{at(0), at(20), at(40)},
			want:   []string{"minute 0", "minute 20", "minute 40"},
		},
		{
			name:   "a Report older than the window is dropped",
			window: 30 * time.Minute,
			limit:  10,
			added:  []entry{at(0), at(20), at(40)},
			want:   []string{"minute 20", "minute 40"},
		},
		{
			name:   "the window is measured against the newest Report",
			window: 10 * time.Minute,
			limit:  10,
			added:  []entry{at(0), at(1), at(2), at(59)},
			want:   []string{"minute 59"},
		},
		{
			name:   "the limit holds when the window has not emptied it",
			window: time.Hour,
			limit:  2,
			added:  []entry{at(0), at(1), at(2), at(3)},
			want:   []string{"minute 2", "minute 3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := ring{window: tt.window, limit: tt.limit}
			for _, e := range tt.added {
				r.add(e)
			}

			var got []string
			for _, message := range r.messages() {
				got = append(got, string(message))
			}
			if len(got) != len(tt.want) {
				t.Fatalf("the ring replays %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("the ring replays %v, want %v", got, tt.want)
					break
				}
			}
		})
	}
}

// The defaults are the ones the Dashboard runs with: an hour of history, and
// a limit a busy cluster cannot push past.
func TestRingDefaultsBoundAnHour(t *testing.T) {
	r := ring{window: ringWindow, limit: ringLimit}

	start := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	// A second apart is as fast as the watch coalesces, so this is the most
	// history a real run can produce.
	for i := range 2 * ringLimit {
		r.add(entry{at: start.Add(time.Duration(i) * time.Second)})
	}

	if len(r.entries) != ringLimit {
		t.Errorf("the ring holds %d Reports, want it capped at %d", len(r.entries), ringLimit)
	}
	if oldest, newest := r.entries[0].at, r.entries[len(r.entries)-1].at; newest.Sub(oldest) > ringWindow {
		t.Errorf("the ring reaches back %v, want no further than %v", newest.Sub(oldest), ringWindow)
	}
}
