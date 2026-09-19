// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

// Package collect reads pods, their top-most owners, those owners' pod
// templates, and failure evidence from the API server, and from manifests
// passed with -f.
//
// It is read-only and never exercises a probe. See
// docs/adr/0002-read-only-against-the-api-server.md.
package collect
