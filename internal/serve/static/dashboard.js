// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

// The placeholder half of the Dashboard: connect to the stream and print
// whichever Report arrived last. What this proves is that the server
// republishes on every change the cluster reports; the drawing comes later.

const elements = {
  status: document.querySelector("#status"),
  initial: document.querySelector("#initial"),
  started: document.querySelector("#started"),
  received: document.querySelector("#received"),
  report: document.querySelector("#report"),
};

// A run served from -f is a cluster as it was written down, so the first
// Report is also the last one. Saying so is the difference between a still
// page and a broken one.
let frozen = false;
let received = 0;

function connected() {
  elements.status.textContent = frozen
    ? "Connected. This run serves one Report read from files, so nothing will change."
    : "Connected. Every change the cluster reports arrives here.";
}

fetch("/api/meta")
  .then((response) => response.json())
  .then((meta) => {
    frozen = meta.static;
    elements.started.textContent = meta.startedAt;
    if (meta.initial) {
      elements.initial.textContent = `the Inspection of ${meta.initial}`;
    }
    connected();
  })
  .catch(() => {
    elements.status.textContent = "The plugin is not answering.";
  });

const stream = new EventSource("/api/events");

stream.addEventListener("open", connected);

// The history observed so far arrives on connect, oldest first, and then one
// message per change. Both are whole Reports, so the newest is the picture.
stream.addEventListener("report", (message) => {
  received += 1;
  elements.received.textContent = String(received);
  elements.report.textContent = message.data;
});

stream.addEventListener("error", () => {
  elements.status.textContent =
    "Disconnected. The plugin may have exited; the browser keeps trying.";
});
