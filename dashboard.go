// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

//go:build dashboard

package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/mertdotcc/kubectl-probes/internal/collect"
	"github.com/mertdotcc/kubectl-probes/internal/serve"
)

// This file is the Dashboard's half of the root command, and the only place
// internal/serve is imported from. It is compiled only with -tags dashboard:
// the released plugin is the CLI alone, and nodashboard.go stands in for it
// there. See docs/adr/0007-the-released-plugin-is-the-cli-alone.md.

// serveDefaultAddr is where --serve binds when it is given no address: a free
// port on loopback, because the Dashboard never authenticates. See
// docs/adr/0006-the-dashboard-ships-inside-the-plugin-binary.md.
const serveDefaultAddr = "127.0.0.1:0"

// dashboardExample is the root command's example for --serve, appended to the
// rest so that a build without the flag has no example for it either.
const dashboardExample = `

  # The Dashboard, in a browser, opened on one workload
  kubectl probes deploy/api --serve`

// dashboard is what --serve was given.
type dashboard struct {
	// addr is where to serve, and empty when --serve was not given.
	addr string
}

func (d *dashboard) addFlags(flags *pflag.FlagSet) {
	// The Dashboard is unauthenticated, so the address is the whole of its
	// security and the help text is where that is said. An optional value
	// keeps the bare --serve meaning the safe thing.
	flags.StringVar(&d.addr, "serve", "",
		"Serve the Dashboard on ADDR, by default a free port on loopback. It is unauthenticated, so an address off loopback hands the cluster's report to the network")
	flags.Lookup("serve").NoOptDefVal = serveDefaultAddr
}

func (d *dashboard) validate(output string) error {
	// The Dashboard is a surface of its own, and it is the JSON encoding it
	// serves. There is no run that is both a pipe and a browser.
	if d.addr != "" && output != outputTable {
		return errors.New("--serve cannot be combined with --output")
	}
	return nil
}

// serve is --serve: the same report, published to a browser and kept current,
// instead of printed once. served is whether --serve was given, and when it
// was, err is how the served run ended.
func (o *options) serve(cmd *cobra.Command, args []string) (served bool, err error) {
	if o.dashboard.addr == "" {
		return false, nil
	}
	return true, o.runServe(cmd, args)
}

// runServe returns when a signal cancels its context, which is the only way a
// served run ends.
func (o *options) runServe(cmd *cobra.Command, args []string) error {
	// A served run lasts until it is interrupted, so it is the one run given a
	// context a signal can cancel. A printed run finishes long before it
	// matters.
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	source, err := o.source(ctx, args)
	if err != nil {
		// A signal that arrived while the watch was still filling its caches
		// is how the user asked to stop, not a failure to report.
		if ctx.Err() != nil {
			return nil
		}
		return err
	}

	// The command takes at most one positional, and it names the workload the
	// Dashboard opens on.
	var initial string
	if len(args) > 0 {
		initial = args[0]
	}

	return serve.Run(ctx, serve.Options{
		Addr:    o.dashboard.addr,
		Initial: initial,
		Analyze: o.analyzeOptions(),
		Source:  source,
		// The URL goes to stderr for the same reason every other note does:
		// a run whose stdout was redirected still says where to look.
		Out: cmd.ErrOrStderr(),
	})
}

// source is where the Dashboard's Reports come from. A set of -f files is a
// cluster as it was written down, so there is nothing in it to watch: it is
// read once and served as it is. See ADR-0003.
func (o *options) source(ctx context.Context, args []string) (serve.Source, error) {
	if len(o.filenames) > 0 {
		result, err := collect.Collect(ctx, o.collectOptions(args))
		if err != nil {
			return serve.Source{}, err
		}
		return serve.Snapshot(result), nil
	}

	results, err := collect.Watch(ctx, o.collectOptions(args))
	if err != nil {
		return serve.Source{}, err
	}
	return serve.Stream(results), nil
}
