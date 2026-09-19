// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package render

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/fatih/color"
)

// The colours every surface uses, and the only ones they use. Colour carries
// meaning here rather than decoration: a fact worth looking at is red, an
// opinion is yellow, and something that is not configured at all is gray.
var (
	red    = color.New(color.FgRed)
	yellow = color.New(color.FgYellow)
	gray   = color.New(color.FgHiBlack)
)

// cell is one table cell: the text that decides how wide its column is, and
// the coloured form that is printed once the width is known.
//
// The two are kept apart because a terminal gives an escape sequence no width
// at all and tabwriter counts every byte of one, so a coloured cell measured
// as it prints pushes its column out by the length of its colour.
type cell struct {
	text string
	// colored is empty for a cell that prints exactly as it measures, which
	// is every cell when colour is disabled.
	colored string
}

// plain is a cell the terminal prints as it is.
func plain(text string) cell { return cell{text: text} }

// tinted is a cell printed in a colour, and a plain one when colour is off.
func tinted(text string, c *color.Color) cell {
	return normalize(cell{text: text, colored: c.Sprint(text)})
}

// join runs cells together into one, each part keeping its own colour. It is
// how a probe cell wears a red drift marker on otherwise plain text.
func join(parts ...cell) cell {
	var out cell
	for _, part := range parts {
		out.text += part.text
		out.colored += part.print()
	}
	return normalize(out)
}

func normalize(c cell) cell {
	if c.colored == c.text {
		c.colored = ""
	}
	return c
}

// print is the cell as it goes to the terminal.
func (c cell) print() string {
	if c.colored == "" {
		return c.text
	}
	return c.colored
}

// table is a grid of cells written through tabwriter, so every column ends up
// as wide as its widest cell and no wider.
type table struct {
	rows []tableRow
}

// tableRow is either a row of cells or a line that is printed as it stands.
type tableRow struct {
	cells []cell
	// raw is a line printed verbatim, taking no part in the column widths and
	// keeping none of its own. It is how the Inspection hangs a probe's latest
	// failure message under the row that counted it, and how it heads a block
	// of rows, without either pushing a column out.
	raw string
	// literal tells the two apart, because a raw line can legitimately be
	// empty and a row of no cells cannot.
	literal bool
}

func (t *table) add(cells ...cell) { t.rows = append(t.rows, tableRow{cells: cells}) }

// addRaw appends a line printed as it is, between the rows around it.
func (t *table) addRaw(line string) {
	t.rows = append(t.rows, tableRow{raw: line, literal: true})
}

// Two spaces between columns, as kubectl's own tables have, and no minimum
// width, so a column of short cells does not take a wide one's room.
const (
	columnPadding  = 2
	columnMinWidth = 0
	columnTabWidth = 0
)

// write aligns the table and writes it out.
//
// Alignment is measured on the uncoloured text and the colour is put back
// afterwards, line by line: every cell sits at a known offset in the aligned
// line, because tabwriter pads a cell on the right and never rewrites it.
//
// Raw lines never reach tabwriter. A line with no tab in it ends every column
// block it falls in, so feeding one through would break the alignment of the
// rows on either side of it, which is the one thing a raw line must not do.
func (t *table) write(w io.Writer) error {
	var aligned bytes.Buffer
	tw := tabwriter.NewWriter(&aligned, columnMinWidth, columnTabWidth, columnPadding, ' ', 0)
	for _, row := range t.rows {
		if row.literal {
			continue
		}
		for i, c := range row.cells {
			if i > 0 {
				fmt.Fprint(tw, "\t")
			}
			fmt.Fprint(tw, c.text)
		}
		fmt.Fprintln(tw)
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	lines := strings.SplitAfter(aligned.String(), "\n")
	next := 0
	for _, row := range t.rows {
		line := row.raw + "\n"
		if !row.literal {
			if next >= len(lines) {
				break
			}
			line = colorize(lines[next], row.cells)
			next++
		}
		if _, err := io.WriteString(w, trimRight(line)); err != nil {
			return err
		}
	}
	return nil
}

// trimRight drops the padding tabwriter puts after the last cell of a line,
// which is invisible on a terminal and noise in a golden file or a diff. The
// padding is plain spaces after everything the cells wrote, colour included,
// so nothing but padding is ever trimmed.
func trimRight(line string) string {
	return strings.TrimRight(line, " \t\n") + "\n"
}

// colorize puts each cell's colour back into an aligned line, leaving the
// padding between them untouched. A line that does not read back as the cells
// it was written from is printed as it is, because losing the colour is better
// than losing the alignment.
func colorize(line string, row []cell) string {
	var out strings.Builder
	pos := 0
	for _, c := range row {
		end := pos + len(c.text)
		if end > len(line) || line[pos:end] != c.text {
			return line
		}
		out.WriteString(c.print())
		pos = end
		for pos < len(line) && line[pos] == ' ' {
			out.WriteByte(' ')
			pos++
		}
	}
	out.WriteString(line[pos:])
	return out.String()
}
