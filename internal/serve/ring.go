// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package serve

import (
	"slices"
	"time"
)

// ringWindow is how far back the observed part of the Timeline reaches. It is
// the cluster's own default event TTL, because an hour is as far back as the
// failure evidence a Report is drawn from survives anyway.
const ringWindow = time.Hour

// ringLimit bounds the ring when a cluster changes faster than the window
// empties it. Coalescing holds the watch to about one Report a second, so an
// hour of a cluster that never settles is a few thousand of them, and no
// browser is made to replay all of them.
const ringLimit = 2000

// ring is the observed part of the Timeline: the Reports published so far,
// oldest first, held as the messages a browser is replayed on connect. It
// lives and dies with the run, because the cluster is the only thing that
// remembers anything past it.
type ring struct {
	window  time.Duration
	limit   int
	entries []entry
}

// entry is one published Report. The time is the Report's own generatedAt,
// kept beside the bytes so that ageing the ring does not mean decoding them
// again.
type entry struct {
	at    time.Time
	frame []byte
}

// add appends a Report and drops what the new one has aged out.
//
// The sweep is measured against the Report being added rather than the wall
// clock, so a ring is what the last hour of reporting held however long ago
// the run reads it back.
func (r *ring) add(e entry) {
	r.entries = append(r.entries, e)

	drop := 0
	for cutoff := e.at.Add(-r.window); drop < len(r.entries); drop++ {
		if !r.entries[drop].at.Before(cutoff) {
			break
		}
	}
	if over := len(r.entries) - drop - r.limit; over > 0 {
		drop += over
	}
	if drop > 0 {
		r.entries = slices.Delete(r.entries, 0, drop)
	}
}

// messages is the history a browser is replayed, oldest first. It is a copy,
// because the ring goes on moving while the browser is being written to.
func (r *ring) messages() [][]byte {
	out := make([][]byte, 0, len(r.entries))
	for _, e := range r.entries {
		out = append(out, e.frame)
	}
	return out
}
