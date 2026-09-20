// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

// The Timeline: the strip along the bottom of the Inspection, the markers on
// it, and the clock that replays them.
//
// The strip has two halves and they are drawn differently on purpose. Up to
// the moment the plugin started serving, everything is reconstructed from the
// timestamps inside the first Report and is hatched; after it, everything was
// observed and is solid. A reader has to be able to tell which half they are
// looking at without being told, because one of them is a replay of what the
// cluster still remembers and the other is what this Dashboard saw. See
// ADR-0005.
//
// Time runs at wall speed unless asked otherwise. Nothing here has a clock of
// its own that produces events: the entries it moves through are the ones
// history.js built out of what the cluster reported.

import { FAILURE, OBSERVED, RECONSTRUCTED, RESTART } from "./history.js";
import { humanDuration } from "./sentences.js";

// The speeds playback offers, wall speed first.
export const SPEEDS = [1, 2, 5, 20];

// The strip is drawn in its own units and stretched to whatever width the
// window gives it, so a marker's position is a share of the span rather than
// a number of pixels.
const trackWidth = 1000;
const trackHeight = 24;

// How long a marker stays lit after the moment it stands for goes by. It is
// the whole of the feedback for somebody who has asked for no motion, so it
// outlasts the pulse it stands in for.
const litMs = 900;

// newTimeline takes over the elements the page laid out and returns what the
// Inspection has to say to it: here is the history, and here is what to do
// when time moves.
export function newTimeline({ root, lane, track, scrub, play, follow, speed, clock, from, to, onDraw, onFire }) {
  const state = {
    entries: [],
    from: 0,
    to: 0,
    startedAt: 0,
    at: 0,
    live: true,
    playing: false,
    speed: 1,
    // still is a run that will never send another Report, which is what a set
    // of -f files is. Nothing about it is live, including the clock.
    still: false,
    // primed says whether the first set of entries has been taken in. Until
    // it has, every entry in the past is history rather than something that
    // has just happened, and none of it is animated.
    primed: false,
    // fired holds the entries already played, so an entry is animated once
    // however many Reports carry it. Scrubbing backwards rebuilds it, which
    // is what lets the same failures be replayed.
    fired: new Set(),
  };

  // markers drawn last time, by the entry each is about.
  let markers = new Map();
  let frame = 0;
  let last = 0;

  scrub.addEventListener("input", () => {
    seek(state.from + (Number(scrub.value) / trackWidth) * span(), { silent: true, follow: false });
  });
  play.addEventListener("click", () => (state.playing ? pause() : start()));
  follow.addEventListener("click", () => {
    pause();
    seek(state.to, { silent: true, follow: true });
  });
  speed.addEventListener("change", () => {
    state.speed = Number(speed.value) || 1;
  });
  lane.addEventListener("click", (event) => {
    const marker = event.target.closest("button.marker");
    if (marker) {
      pause();
      seek(Number(marker.dataset.at), { silent: true, follow: false });
    }
  });

  drawTrack(track);

  return {
    // update takes the history as it now stands. While the Timeline is live it
    // moves to the newest moment and plays whatever arrived with it, which is
    // how a failure reaching the cluster reaches the drawing.
    update(entries, extent) {
      state.entries = entries;
      state.from = extent.from;
      state.to = extent.to;
      state.startedAt = extent.startedAt;
      // The strip is laid out here rather than on every frame: a marker moves
      // when the history changes under it, not when the clock advances
      // through it.
      sizeTrack();
      drawMarkers();
      sayExtent();

      if (!state.primed) {
        state.primed = true;
        seek(state.to, { silent: true, follow: true });
        return;
      }
      if (state.live) {
        seek(state.to, { silent: false, follow: true });
        return;
      }
      draw();
    },

    // reset is a different workload, which is a different history: nothing
    // that has been played is about this one.
    reset() {
      pause();
      state.primed = false;
      state.fired = new Set();
      state.live = true;
      markers = new Map();
      lane.replaceChildren();
    },

    // moment is the point in time the diagram should be drawn for.
    moment() {
      return state.at;
    },

    following() {
      return state.live;
    },

    // still says the Timeline has nothing to replay, which is what a run
    // served from files is: a cluster as it was written down has no runtime
    // state and no history. See ADR-0003.
    still(yes) {
      state.still = yes;
      root.dataset.still = String(yes);
    },
  };

  function span() {
    return Math.max(1, state.to - state.from);
  }

  // seek moves to a moment. Silent means the entries up to it are marked
  // played without being played: a tab opening on an hour of history should
  // not replay the hour. Following means the Timeline is tracking the newest
  // Report rather than a moment the reader chose.
  function seek(moment, { silent, follow }) {
    const was = state.at;
    state.at = Math.min(Math.max(moment, state.from), state.to);
    state.live = follow;

    if (silent) {
      state.fired = new Set(
        state.entries.filter((entry) => entry.at <= state.at).map((entry) => entry.key),
      );
    } else {
      for (const entry of state.entries) {
        if (entry.at > state.at || state.fired.has(entry.key)) {
          continue;
        }
        state.fired.add(entry.key);
        // An entry older than where the clock already was is one that has
        // only just been reported; it is still news, and it is played.
        if (entry.at > was || state.live) {
          fire(entry);
        }
      }
    }
    draw();
  }

  function fire(entry) {
    const marker = markers.get(entry.key);
    if (marker) {
      marker.dataset.lit = "true";
      setTimeout(() => marker.removeAttribute("data-lit"), litMs);
    }
    onFire(entry);
  }

  function start() {
    if (state.playing) {
      return;
    }
    // Playing from the end is playing again, which is the only thing the
    // button can usefully mean there.
    if (state.at >= state.to) {
      seek(state.from, { silent: true, follow: false });
    }
    state.playing = true;
    state.live = false;
    last = performance.now();
    frame = requestAnimationFrame(step);
    draw();
  }

  function pause() {
    state.playing = false;
    cancelAnimationFrame(frame);
    draw();
  }

  function step(now) {
    const moved = (now - last) * state.speed;
    last = now;
    const next = state.at + moved;
    if (next >= state.to) {
      // Catching up with the newest Report is catching up with the cluster,
      // so playback hands back to live rather than stopping at the edge.
      state.playing = false;
      seek(state.to, { silent: false, follow: true });
      return;
    }
    seek(next, { silent: false, follow: false });
    frame = requestAnimationFrame(step);
  }

  function draw() {
    const position = ((state.at - state.from) / span()) * trackWidth;
    scrub.max = String(trackWidth);
    scrub.value = String(Math.round(position));
    scrub.setAttribute("aria-valuetext", momentWords(state));

    track.querySelector(".playhead").setAttribute("x", String(position));
    track.querySelector(".played").setAttribute("width", String(Math.max(0, position)));
    root.dataset.playing = String(state.playing);
    root.dataset.live = String(state.live);
    play.textContent = state.playing ? "Pause" : "Play";
    play.setAttribute("aria-pressed", String(state.playing));
    follow.setAttribute("aria-pressed", String(state.live));
    clock.textContent = momentWords(state);

    onDraw(state.at);
  }

  // sayExtent says what the strip covers at either end of it. The span reaches
  // back as far as the oldest timestamp in the first Report, which for a pod
  // that has been up for months is months, and a scrubber whose scale nobody
  // can read is a bar.
  function sayExtent() {
    from.textContent = new Date(state.from).toLocaleString();
    to.textContent = state.still ? "" : "now";
  }

  // sizeTrack sets how much of the strip is the hatched half: everything
  // before the plugin started serving, which is as far back as the first
  // Report's timestamps reach and no further.
  function sizeTrack() {
    const width = Math.min(Math.max(state.startedAt - state.from, 0), span());
    track
      .querySelector(".reconstructed")
      .setAttribute("width", String((width / span()) * trackWidth));
  }

  function drawMarkers() {
    const next = new Map();
    for (const entry of state.entries) {
      const marker = markers.get(entry.key) ?? newMarker(entry);
      marker.style.setProperty("--at", `${(((entry.at - state.from) / span()) * 100).toFixed(3)}%`);
      if (marker.parentElement === null) {
        lane.append(marker);
      }
      next.set(entry.key, marker);
      markers.delete(entry.key);
    }
    // Whatever is left drew an entry the history no longer has, which happens
    // when the hour the server replays has moved on past it.
    for (const stale of markers.values()) {
      stale.remove();
    }
    markers = next;
  }

  function newMarker(entry) {
    const marker = document.createElement("button");
    marker.type = "button";
    marker.className = "marker";
    marker.dataset.kind = entry.kind;
    marker.dataset.source = entry.source;
    marker.dataset.at = String(entry.at);
    if (entry.kind === FAILURE && entry.probe) {
      marker.dataset.probe = entry.probe;
    }
    const words = markerWords(entry);
    marker.title = words;
    marker.setAttribute("aria-label", words);
    return marker;
  }
}

// markerWords is the tooltip: when it happened, and the sentence the CLI would
// have printed for it. The wording is the cluster's own wherever the cluster
// wrote one, which for a probe failure is the kubelet's message verbatim.
export function markerWords(entry) {
  const when = new Date(entry.at).toLocaleString();
  const what = entry.text || entry.kind;
  const parts = [`${when} — ${what}`];
  if (entry.note) {
    parts.push(`(${entry.note})`);
  }
  if (entry.source === RECONSTRUCTED) {
    parts.push("(reconstructed from cluster timestamps)");
  }
  return parts.join(" ");
}

// momentWords says where the clock is, in words, for the label beside the
// controls and for a reader who cannot see the strip.
function momentWords(state) {
  if (state.still) {
    // A run read from files is as old as the files. Calling it live would be
    // the one thing a reader could take from this label that is not true.
    return new Date(state.to).toLocaleString();
  }
  if (state.live) {
    return "live";
  }
  const behind = state.to - state.at;
  const when = new Date(state.at).toLocaleTimeString();
  return behind < 1000 ? when : `${when} (${humanDuration(behind)} ago)`;
}

// drawTrack lays out the strip itself: the span before the plugin started,
// hatched, and the span since, solid, with the part already played filled in
// behind the playhead.
//
// The two halves are drawn as one rectangle each in the strip's own units and
// stretched to the width on screen, so nothing here has to know how wide the
// window is.
function drawTrack(track) {
  track.setAttribute("viewBox", `0 0 ${trackWidth} ${trackHeight}`);
  track.setAttribute("preserveAspectRatio", "none");
  track.setAttribute("aria-hidden", "true");

  const hatch = svg("pattern", {
    id: "hatch",
    width: "6",
    height: "6",
    patternUnits: "userSpaceOnUse",
    patternTransform: "rotate(45)",
  });
  hatch.append(svg("line", { x1: "0", y1: "0", x2: "0", y2: "6", "stroke-width": "2" }));
  const defs = svg("defs", {});
  defs.append(hatch);

  // The hatching goes on last so that it stays visible over the part already
  // played: it is lines with gaps between them, and what is underneath shows
  // through. A reader scrubbing back through the reconstructed half has to be
  // able to see that it is still the reconstructed half.
  track.replaceChildren(
    defs,
    svg("rect", { class: "observed", x: "0", y: "0", width: String(trackWidth), height: String(trackHeight) }),
    svg("rect", { class: "played", x: "0", y: "0", width: "0", height: String(trackHeight) }),
    svg("rect", { class: "reconstructed", x: "0", y: "0", width: "0", height: String(trackHeight), fill: "url(#hatch)" }),
    svg("rect", { class: "playhead", x: "0", y: "0", width: "2", height: String(trackHeight) }),
  );
}

function svg(name, attributes) {
  const element = document.createElementNS("http://www.w3.org/2000/svg", name);
  for (const [key, value] of Object.entries(attributes)) {
    element.setAttribute(key, value);
  }
  return element;
}

// kinds is what the legend has to explain, in the order the strip is read.
export const MARKER_KINDS = [
  { kind: FAILURE, said: "an Unhealthy event the cluster reported" },
  { kind: RESTART, said: "a container that died and came back" },
  { kind: "condition", said: "a pod condition that flipped" },
];

export const SOURCE_WORDS = {
  [OBSERVED]: "observed: a Report this Dashboard received",
  [RECONSTRUCTED]: "reconstructed from cluster timestamps",
};
