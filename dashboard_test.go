// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

//go:build dashboard

package main

import (
	"strings"
	"testing"
)

// --serve is a surface, not a format: it serves the JSON encoding itself, so
// there is no run that is both a pipe and a browser.
func TestRunRejectsServeWithOutput(t *testing.T) {
	for _, output := range []string{"json", "yaml", "wide"} {
		t.Run(output, func(t *testing.T) {
			_, _, err := execute(append([]string{"--serve", "-o", output}, oneWorkload...)...)
			if err == nil {
				t.Fatal("--serve was combined with --output")
			}
			if want := "--serve cannot be combined with --output"; !strings.Contains(err.Error(), want) {
				t.Errorf("error = %q, want it to contain %q", err, want)
			}
		})
	}
}

// The Dashboard never authenticates, so the address it binds is the whole of
// its security and the help text is where a user is told so.
func TestServeHelpNamesLoopback(t *testing.T) {
	stdout, _ := run(t, "--help")

	usage, found := flagUsage(stdout, "--serve")
	if !found {
		t.Fatalf("--help does not list --serve:\n%s", stdout)
	}
	if !strings.Contains(usage, "loopback") {
		t.Errorf("--serve is described as %q, want it to say where it binds", usage)
	}
	// The address is optional, and the bare flag is the safe one.
	if !strings.Contains(usage, `[="127.0.0.1:0"]`) {
		t.Errorf("--serve is listed as %q, want the bare flag to default to loopback", usage)
	}
}
