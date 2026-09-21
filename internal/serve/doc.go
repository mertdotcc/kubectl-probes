// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

// Package serve is the server half of the Dashboard: it publishes the same
// v1alpha1 Report the CLI prints with -o json, keeps it current from a watch,
// and serves the Dashboard's own assets out of the binary.
//
// It binds loopback and never authenticates, because the only person who can
// reach it is the one who ran the plugin. See
// docs/adr/0006-the-dashboard-ships-inside-the-plugin-binary.md.
//
// Only a build with -tags dashboard imports it. The released plugin is the CLI
// alone and carries neither this server nor its assets; see
// docs/adr/0007-the-released-plugin-is-the-cli-alone.md.
package serve
