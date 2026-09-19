// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package render

import (
	"errors"
	"io"
	"testing"

	"github.com/fatih/color"
)

// errWriter is an output that has gone away, which in practice is a closed
// pipe: kubectl probes | head.
type errWriter struct{}

var errClosed = errors.New("write: broken pipe")

func (errWriter) Write([]byte) (int, error) { return 0, errClosed }

func TestOverviewReportsWriteErrors(t *testing.T) {
	tests := []struct {
		name        string
		out, errOut io.Writer
		emptyReport bool
	}{
		{name: "the table cannot be written", out: errWriter{}, errOut: io.Discard},
		{name: "a note cannot be written", out: io.Discard, errOut: errWriter{}},
		{name: "the empty result cannot be reported", out: io.Discard, errOut: errWriter{}, emptyReport: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := sampleReport()
			if tt.emptyReport {
				report.Workloads = nil
			}
			if err := Overview(tt.out, tt.errOut, report, Options{}); !errors.Is(err, errClosed) {
				t.Errorf("Overview error = %v, want %v", err, errClosed)
			}
		})
	}
}

// A line that does not read back as the cells it was written from keeps its
// alignment and loses its colour, which is the cheaper of the two to lose.
func TestColorizeLeavesUnrecognizedLinesAlone(t *testing.T) {
	color.NoColor = false
	t.Cleanup(func() { color.NoColor = true })

	line := "something else entirely\n"
	if got := colorize(line, []cell{tinted("api", red)}); got != line {
		t.Errorf("colorize() = %q, want %q", got, line)
	}
}
