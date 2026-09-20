// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package serve

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/mertdotcc/kubectl-probes/internal/analyze"
	"github.com/mertdotcc/kubectl-probes/internal/collect"
)

// shutdownGrace is how long a browser still holding the stream open has to
// let go before the plugin exits anyway. Ctrl-C is an instruction, not a
// request, and nothing served here is worth waiting on.
const shutdownGrace = 2 * time.Second

// Options is what --serve asked for, in the terms this package needs.
type Options struct {
	// Addr is the address to listen on. The default is a free port on
	// loopback, because the Dashboard never authenticates: the only person
	// who can reach it is the one who ran the plugin. See ADR-0006.
	Addr string
	// Initial is the positional argument the run was given, so the Dashboard
	// opens on that workload's Inspection. Empty opens the Overview.
	Initial string
	// Analyze is what the flags mean to the analyze package, so a Report
	// served here says exactly what -o json would have printed.
	Analyze analyze.Options
	// Source is where the Reports come from.
	Source Source
	// Out is where the URL is announced. The command passes its stderr, so a
	// user who redirected stdout is still told where to point a browser.
	// Nil is os.Stderr.
	Out io.Writer
}

// Source is where a server's Reports come from: a watch that keeps producing
// Results, or the single Result a set of -f files reduces to.
type Source struct {
	results <-chan *collect.Result
	static  *collect.Result
}

// Stream is a Source that follows a cluster, as collect.Watch reports it.
func Stream(results <-chan *collect.Result) Source {
	return Source{results: results}
}

// Snapshot is a Source frozen at one Result, which is what -f serves: a set
// of files is a cluster as it was written down, and a manifest has no runtime
// state to change. See ADR-0003 and ADR-0006.
func Snapshot(result *collect.Result) Source {
	return Source{static: result}
}

// frozen reports whether the Source will ever produce another Result, which
// is what /api/meta tells the browser so it knows not to wait for one.
func (s Source) frozen() bool {
	return s.results == nil
}

// Run serves the Dashboard until ctx is cancelled, which is what a SIGINT or
// a SIGTERM reaching the plugin comes to.
func Run(ctx context.Context, o Options) error {
	out := o.Out
	if out == nil {
		out = os.Stderr
	}

	ln, err := net.Listen("tcp", o.Addr)
	if err != nil {
		return err
	}
	defer ln.Close()

	s := newServer(o)
	// The first Report is published before the URL is, so a browser opening
	// it the moment it appears is never told there is nothing to show yet.
	if !s.begin(ctx) {
		return nil
	}

	fmt.Fprintf(out, "Serving on http://%s\n", ln.Addr())
	if o.Initial != "" {
		fmt.Fprintf(out, "Opening on %s\n", o.Initial)
	}

	srv := &http.Server{Handler: s.handler()}
	serving := make(chan error, 1)
	go func() { serving <- srv.Serve(ln) }()
	if !o.Source.frozen() {
		go s.follow(ctx)
	}

	select {
	case err := <-serving:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		return s.shutdown(srv)
	}
}

// shutdown closes the server down within shutdownGrace.
//
// The streams are told to let go first: an SSE handler is a request that
// never finishes on its own, and Shutdown waits for the requests in flight.
func (s *server) shutdown(srv *http.Server) error {
	close(s.done)

	ctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutting the Dashboard down: %w", err)
	}
	return nil
}
