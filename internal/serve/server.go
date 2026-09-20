// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package serve

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"sync"
	"time"

	"k8s.io/klog/v2"

	"github.com/mertdotcc/kubectl-probes/internal/analyze"
	"github.com/mertdotcc/kubectl-probes/internal/collect"
	"github.com/mertdotcc/kubectl-probes/internal/model"
)

// The Dashboard's own assets, which is what makes the plugin one binary: krew
// installs it and the browser side is already inside. See ADR-0006.
//
//go:embed static
var assets embed.FS

// keepaliveInterval is how often a stream with nothing to say says so, which
// is what keeps a proxy or an impatient browser from taking a still cluster
// for a dead server. It is a variable so a test need not wait out a quarter
// of a minute to see one.
var keepaliveInterval = 15 * time.Second

// clientBacklog is how many Reports a browser may be behind before the server
// stops holding them. One is the picture it is drawing and the rest are the
// ones it has yet to draw; a browser that cannot keep up with a handful is
// not going to catch up with a hundred.
const clientBacklog = 8

// server is one run of the Dashboard: the Reports published so far, and the
// browsers being told about them.
type server struct {
	opts      Options
	startedAt time.Time
	// done is closed when the run is shutting down, which is how a stream
	// that would otherwise never finish is told to let go.
	done chan struct{}

	mu sync.Mutex
	// latest is the newest Report as -o json prints it, which is what
	// /api/report answers with.
	latest []byte
	// ring is the observed part of the Timeline, held as the messages a
	// browser is replayed on connect.
	ring    ring
	clients map[*client]struct{}
}

// client is one browser on the stream. It is the channel and nothing else:
// what a browser is owed is the Reports published since it connected.
type client struct {
	updates chan []byte
}

func newServer(o Options) *server {
	return &server{
		opts:      o,
		startedAt: time.Now(),
		done:      make(chan struct{}),
		ring:      ring{window: ringWindow, limit: ringLimit},
		clients:   map[*client]struct{}{},
	}
}

func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServer(http.FS(pages())))
	mux.HandleFunc("GET /api/report", s.report)
	mux.HandleFunc("GET /api/events", s.events)
	mux.HandleFunc("GET /api/meta", s.meta)
	return secure(mux)
}

// pages is the Dashboard's assets at the root of their own filesystem, so
// GET / is index.html rather than /static/index.html.
func pages() fs.FS {
	sub, err := fs.Sub(assets, "static")
	if err != nil {
		// The directory is embedded at build time. It is there, or this
		// package did not compile.
		panic(err)
	}
	return sub
}

// secure sets the headers every response carries. The Dashboard talks only to
// itself, so a policy strict enough to forbid inline script costs nothing and
// keeps a probe failure message from ever being read as one.
func secure(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		h.ServeHTTP(w, r)
	})
}

// begin publishes the first Report, the one a browser sees the moment it
// connects. It reports whether there is still a run to serve.
func (s *server) begin(ctx context.Context) bool {
	if s.opts.Source.frozen() {
		s.publish(s.opts.Source.static)
		return true
	}
	select {
	case result, ok := <-s.opts.Source.results:
		if !ok {
			return false
		}
		s.publish(result)
		return true
	case <-ctx.Done():
		return false
	}
}

// follow publishes a Report for every change the cluster reports, until the
// watch behind it ends. A frozen Source has no watch and never reaches here.
//
// The watch has already coalesced the bursts, so what arrives here is a state
// the cluster settled on, and is published as it comes.
func (s *server) follow(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case result, ok := <-s.opts.Source.results:
			if !ok {
				return
			}
			s.publish(result)
		}
	}
}

// publish analyzes and encodes one Result, once. Every browser on the stream
// is sent the same bytes, and they are the bytes -o json would have printed
// for the same cluster.
func (s *server) publish(result *collect.Result) {
	report := analyze.Analyze(result, time.Now(), s.opts.Analyze)

	var buf bytes.Buffer
	if err := report.Encode(&buf, model.FormatJSON); err != nil {
		// A Report that will not encode is this run's bug, not the cluster's,
		// and the next change is as likely to encode as this one was.
		klog.V(2).Infof("encoding a Report for the Dashboard failed: %v", err)
		return
	}
	encoded := buf.Bytes()
	frame := messageOf(encoded)

	s.mu.Lock()
	defer s.mu.Unlock()

	s.latest = encoded
	s.ring.add(entry{at: report.GeneratedAt, frame: frame})
	for c := range s.clients {
		select {
		case c.updates <- frame:
		default:
			// A browser that has fallen behind is dropped rather than waited
			// for. Holding the publisher up for one tab would stall the watch
			// for every other, and what that tab missed is a state the
			// cluster has already left behind.
			s.drop(c)
		}
	}
}

// join registers a browser and takes the history it is owed in one step, so a
// Report published between the two reaches it once rather than twice or not
// at all.
func (s *server) join() ([][]byte, *client) {
	s.mu.Lock()
	defer s.mu.Unlock()

	c := &client{updates: make(chan []byte, clientBacklog)}
	s.clients[c] = struct{}{}
	return s.ring.messages(), c
}

func (s *server) leave(c *client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.drop(c)
}

// drop forgets a browser and closes the channel it was being told through,
// which is how its handler learns it is over. It must be called with the lock
// held, and closes the channel once however often it is called.
func (s *server) drop(c *client) {
	if _, ok := s.clients[c]; !ok {
		return
	}
	delete(s.clients, c)
	close(c.updates)
}

// report answers with the newest Report, byte for byte what -o json prints.
func (s *server) report(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	latest := s.latest
	s.mu.Unlock()

	if latest == nil {
		http.Error(w, "no Report has been published yet", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(latest)
}

// meta is what the browser needs before the first Report: where the observed
// part of the Timeline begins, which Inspection to open, and whether anything
// will ever change.
type meta struct {
	StartedAt time.Time `json:"startedAt"`
	Initial   string    `json:"initial"`
	Static    bool      `json:"static"`
}

func (s *server) meta(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(meta{
		StartedAt: s.startedAt.UTC(),
		Initial:   s.opts.Initial,
		Static:    s.opts.Source.frozen(),
	}); err != nil {
		klog.V(2).Infof("writing the Dashboard's metadata failed: %v", err)
	}
}

// events is the stream: the Reports observed so far, oldest first, and then
// every Report published while the browser stays connected.
func (s *server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "this server cannot stream", http.StatusInternalServerError)
		return
	}

	header := w.Header()
	header.Set("Content-Type", "text/event-stream")
	// Nothing on this stream is worth reading twice, and a cached Report is a
	// Report that is no longer true.
	header.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	history, c := s.join()
	defer s.leave(c)

	for _, message := range history {
		if _, err := w.Write(message); err != nil {
			return
		}
	}
	flusher.Flush()

	keepalive := time.NewTicker(keepaliveInterval)
	defer keepalive.Stop()

	for {
		select {
		case message, ok := <-c.updates:
			if !ok {
				// Dropped for falling behind. The browser reconnects and is
				// replayed the history it missed.
				return
			}
			if _, err := w.Write(message); err != nil {
				return
			}
		case <-keepalive.C:
			if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil {
				return
			}
		case <-r.Context().Done():
			return
		case <-s.done:
			return
		}
		flusher.Flush()
	}
}

// messageOf frames a Report as one SSE message. The encoding is indented, so
// every line of it carries the data prefix of its own.
func messageOf(report []byte) []byte {
	var b bytes.Buffer
	b.WriteString("event: report\n")
	for _, line := range bytes.Split(bytes.TrimRight(report, "\n"), []byte("\n")) {
		b.WriteString("data: ")
		b.Write(line)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	return b.Bytes()
}
