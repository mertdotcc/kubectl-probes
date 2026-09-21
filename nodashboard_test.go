// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

//go:build !dashboard

package main

import (
	"regexp"
	"strings"
	"testing"
)

// The released plugin is the CLI alone, so there is no --serve anywhere a user
// could find one: not in the flag list, not in an example, and not accepted on
// the command line. See docs/adr/0007-the-released-plugin-is-the-cli-alone.md.
func TestReleasedPluginHasNoServe(t *testing.T) {
	stdout, _ := run(t, "--help")
	// On a word boundary, because kubectl's own --server is in the list too.
	for _, absent := range []string{`--serve\b`, `Dashboard`} {
		if regexp.MustCompile(absent).MatchString(stdout) {
			t.Errorf("--help matches %s:\n%s", absent, stdout)
		}
	}

	_, _, err := execute(append([]string{"--serve"}, oneWorkload...)...)
	if err == nil {
		t.Fatal("--serve was accepted")
	}
	if want := "unknown flag: --serve"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err, want)
	}
}
