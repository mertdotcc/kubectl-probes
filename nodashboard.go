// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

//go:build !dashboard

package main

import (
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// This file is the released plugin's half of the root command, which is the
// CLI alone: there is no --serve to register, nothing about it to validate,
// and no served run. The Dashboard is built with -tags dashboard, from
// dashboard.go. See docs/adr/0007-the-released-plugin-is-the-cli-alone.md.

const dashboardExample = ""

type dashboard struct{}

func (*dashboard) addFlags(*pflag.FlagSet) {}

func (*dashboard) validate(string) error { return nil }

func (*options) serve(*cobra.Command, []string) (served bool, err error) {
	return false, nil
}
