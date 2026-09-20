// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

// The Inspection, as far as this ticket takes it: the panel, the way into it
// from a row of the Overview, and the way back out. What it draws is the
// workload exactly as the newest Report carries it, which is the one thing
// that is certainly true and certainly not invented.
//
// The kubelet-and-pods drawing and the Timeline replace the body of this
// panel; see issue #35 and ADR-0005.

import { workloadNamed } from "./overview.js";

const placeholder =
  "The Inspection is not drawn yet. Until it is, this is the workload as the newest Report carries it.";

// newInspection takes over the panel the page laid out and returns the one
// thing the rest of the Dashboard wants from it: draw.
export function newInspection({ panel, name, note, body, close, onClose }) {
  close.addEventListener("click", onClose);

  return function draw(report, selected) {
    panel.hidden = selected === "";
    if (panel.hidden) {
      return;
    }

    name.textContent = selected;
    const workload = workloadNamed(report, selected);
    if (!workload) {
      // Either the run was opened on a name that covers more than one
      // workload, or the workload has since left the cluster. Saying which
      // Report was looked in is the difference between the two.
      note.textContent = `No workload named ${selected} is in the newest Report.`;
      body.textContent = "";
      return;
    }
    note.textContent = placeholder;
    body.textContent = JSON.stringify(workload, null, 2);
  };
}
