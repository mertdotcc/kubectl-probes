// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

// The Overview's grid. It is handed rows that already know what they say and
// puts them on screen, which is the division internal/render/table.go makes
// for the same reason: what goes in a cell is one question and how a cell is
// drawn is another.
//
// A Report arrives whenever the cluster changes, so the grid is written into
// rather than thrown away: a reader who has scrolled, filtered or sorted
// keeps all three, and only the cells that actually changed move.

import { DRIFT_MARK, cellsOf, columnsFor } from "./overview.js";

// highlightMs must match the animation in app.css, which is what the reader
// actually sees. The class is taken off on a timer rather than on the
// animation ending, because somebody who has asked for no motion never gets
// an animation to end.
const highlightMs = 600;
const changedClass = "changed";

// The timer holding each cell's highlight up, so a cell that changes twice
// inside one highlight gets the whole of a second one rather than the tail of
// the first.
const highlights = new WeakMap();

// newTable takes over the elements the page laid out and returns the one
// thing the rest of the Dashboard wants from them: draw.
export function newTable({ table, head, body, empty, onSelect }) {
  // The listeners go on the body, which outlives every row in it. Rows are
  // moved and reused as Reports arrive, and a listener per row would have to
  // be hung and taken down with them.
  body.addEventListener("click", (event) => {
    const row = event.target.closest("tr[data-workload]");
    if (row) {
      onSelect(row.dataset.workload);
    }
  });
  body.addEventListener("keydown", (event) => {
    if (event.key !== "Enter" && event.key !== " ") {
      return;
    }
    const row = event.target.closest("tr[data-workload]");
    if (row) {
      // Space scrolls the page otherwise, which is not what a reader who has
      // just chosen a row meant by it.
      event.preventDefault();
      onSelect(row.dataset.workload);
    }
  });

  // The rows drawn last time, by the key that names the container each is
  // about, so this time's rows can be the same elements moved.
  let drawn = new Map();
  let columns = "";

  return function draw({ rows, namespaced, selected, note }) {
    const wanted = columnsFor(namespaced);
    if (wanted.join(" ") !== columns) {
      // A Report that started or stopped spanning namespaces is a different
      // table, not a changed one, and every cell in it has moved a column.
      columns = wanted.join(" ");
      drawHead(head, wanted);
      body.replaceChildren();
      drawn = new Map();
    }

    empty.textContent = note;
    empty.hidden = note === "";
    table.hidden = rows.length === 0;

    const next = new Map();
    let cursor = body.firstElementChild;
    for (const row of rows) {
      const redrawn = drawn.has(row.key);
      const drawnRow = drawn.get(row.key) ?? newRow(wanted.length, row);
      fill(drawnRow, row, namespaced, redrawn);
      drawnRow.setAttribute("aria-selected", String(row.workload === selected));

      if (drawnRow === cursor) {
        cursor = cursor.nextElementSibling;
      } else {
        body.insertBefore(drawnRow, cursor);
      }
      next.set(row.key, drawnRow);
    }
    // Whatever the cursor did not reach is a container the newest Report no
    // longer has, so it leaves the table with the pods it described.
    while (cursor) {
      const after = cursor.nextElementSibling;
      cursor.remove();
      cursor = after;
    }
    drawn = next;
  };
}

function drawHead(head, columns) {
  head.replaceChildren(
    ...columns.map((name) => {
      const th = document.createElement("th");
      th.scope = "col";
      th.textContent = name;
      return th;
    }),
  );
}

function newRow(width, row) {
  const tr = document.createElement("tr");
  // The workload is what a row is a way into, and the hash is written in its
  // display name: the same string the CLI prints and accepts back.
  tr.dataset.workload = row.workload;
  tr.tabIndex = 0;
  for (let i = 0; i < width; i++) {
    tr.append(document.createElement("td"));
  }
  return tr;
}

// fill writes a row's cells, and holds up the ones whose value changed. A row
// being drawn for the first time changes nothing: it is all new, and
// highlighting all of it would say nothing about what moved.
function fill(tr, row, namespaced, redrawn) {
  const cells = cellsOf(row, namespaced);
  for (const [i, cell] of cells.entries()) {
    const td = tr.children[i];
    const value = cell.text + (cell.mark ? DRIFT_MARK : "");
    if (td.dataset.value === value) {
      continue;
    }
    if (redrawn) {
      highlight(td);
    }
    td.dataset.value = value;
    td.dataset.tone = cell.tone;
    td.textContent = cell.text;
    if (cell.mark) {
      td.append(mark());
    }
  }
}

// mark is the drift star. It is an element of its own because only the star
// is coloured; the handler and duration beside it are plain, as they are in
// the terminal.
function mark() {
  const span = document.createElement("span");
  span.className = "mark";
  span.textContent = DRIFT_MARK;
  return span;
}

function highlight(td) {
  clearTimeout(highlights.get(td));
  td.classList.remove(changedClass);
  // Reading the layout back is what restarts an animation that is already
  // running. Without it a cell changing twice in a row flashes once.
  void td.offsetWidth;
  td.classList.add(changedClass);
  highlights.set(
    td,
    setTimeout(() => td.classList.remove(changedClass), highlightMs),
  );
}
