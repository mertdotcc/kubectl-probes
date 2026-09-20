// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package serve

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/mertdotcc/kubectl-probes/internal/collect"
	"github.com/mertdotcc/kubectl-probes/internal/model"
)

// The stream tests assert about something arriving rather than something
// returning, so every wait below has a window: long enough that a loaded
// machine does not fail a test, short enough that a genuine failure is not a
// coffee break.
const arrives = 5 * time.Second

// reported is a Result holding one workload, named so that a test can tell
// one Report in the stream from the next.
func reported(name string) *collect.Result {
	return &collect.Result{Workloads: []collect.Workload{{
		GroupKind: schema.GroupKind{Group: "apps", Kind: "Deployment"},
		Name:      name,
		Namespace: "prod",
	}}}
}

// serving is a server with its endpoints in front of a listener, torn down in
// the order that lets a stream let go before the listener waits for it.
func serving(t *testing.T, o Options) (*server, string) {
	t.Helper()
	s := newServer(o)
	ts := httptest.NewServer(s.handler())
	t.Cleanup(ts.Close)
	t.Cleanup(func() { close(s.done) })
	return s, ts.URL
}

// get reads one response in full, and fails the test rather than returning a
// body nobody checked the status of.
func get(t *testing.T, url string) (*http.Response, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s failed: %v", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading GET %s failed: %v", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s is %s:\n%s", url, resp.Status, body)
	}
	return resp, string(body)
}

// message is one SSE message as a browser sees it: an event with its data,
// with the per-line prefixes taken off, or a comment such as the keepalive.
type message struct {
	event   string
	data    string
	comment string
}

// listen opens the stream and reads it in the background, because a read that
// waits for a message that never comes must fail the test rather than hang it.
func listen(t *testing.T, url string) <-chan message {
	t.Helper()
	resp, err := http.Get(url + "/api/events")
	if err != nil {
		t.Fatalf("connecting to the stream failed: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })

	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("the stream is %q, want text/event-stream", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("the stream is cached as %q, want no-store", got)
	}

	messages := make(chan message, 16)
	go func() {
		defer close(messages)
		reader := bufio.NewReader(resp.Body)
		for {
			m, ok := read(reader)
			if !ok {
				return
			}
			messages <- m
		}
	}()
	return messages
}

// read takes one message off the stream, up to the blank line that ends it.
func read(r *bufio.Reader) (message, bool) {
	var m message
	var data []string
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return message{}, false
		}
		line = strings.TrimSuffix(line, "\n")
		switch {
		case line == "":
			m.data = strings.Join(data, "\n")
			return m, true
		case strings.HasPrefix(line, ":"):
			m.comment = strings.TrimSpace(strings.TrimPrefix(line, ":"))
		case strings.HasPrefix(line, "event: "):
			m.event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data = append(data, strings.TrimPrefix(line, "data: "))
		}
	}
}

// next takes the next message, and says what was missing rather than hanging.
func next(t *testing.T, messages <-chan message, what string) message {
	t.Helper()
	select {
	case m, ok := <-messages:
		if !ok {
			t.Fatalf("the stream ended before %s arrived", what)
		}
		return m
	case <-time.After(arrives):
		t.Fatalf("%s did not arrive within %v", what, arrives)
		return message{}
	}
}

// workloadIn is the one workload a Report names, which is how a test tells
// which Result a message carries.
func workloadIn(t *testing.T, encoded string) string {
	t.Helper()
	var report model.Report
	if err := json.Unmarshal([]byte(encoded), &report); err != nil {
		t.Fatalf("the Report does not parse: %v\n%s", err, encoded)
	}
	if len(report.Workloads) != 1 {
		t.Fatalf("the Report holds %d workloads, want the one published", len(report.Workloads))
	}
	return report.Workloads[0].DisplayName
}

// /api/report is the CLI's own output over HTTP: the newest Report, in the
// bytes -o json prints.
func TestReportIsTheLatestSnapshot(t *testing.T) {
	s, url := serving(t, Options{Source: Stream(make(chan *collect.Result))})

	// Run publishes before it serves, so this is only ever the answer to a
	// request that beat the first Result. It is still an answer and not an
	// empty Report, because there is no such thing as a Report of nothing.
	if resp, err := http.Get(url + "/api/report"); err != nil {
		t.Fatalf("GET /api/report failed: %v", err)
	} else {
		resp.Body.Close()
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("a Report was served as %s before one was published", resp.Status)
		}
	}

	s.publish(reported("api"))
	s.publish(reported("edge"))

	resp, body := get(t, url+"/api/report")
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("the Report is served as %q, want application/json", got)
	}
	if got := workloadIn(t, body); got != "deploy/edge" {
		t.Errorf("the Report names %s, want the newest one published", got)
	}
	if body != string(s.latest) {
		t.Errorf("the Report served is not the bytes that were encoded:\n%s", body)
	}
	// -o json is indented, and a consumer that diffs two runs of it is why.
	if !strings.Contains(body, "\n  \"kind\": \"ProbeReport\"") {
		t.Errorf("the Report is not indented the way -o json prints it:\n%s", body)
	}
}

// A browser is owed what was observed before it opened the tab, and then
// everything that happens while it is watching.
func TestEventsReplaysThenStreams(t *testing.T) {
	s, url := serving(t, Options{Source: Stream(make(chan *collect.Result))})

	s.publish(reported("api"))
	s.publish(reported("edge"))

	messages := listen(t, url)

	for _, want := range []string{"deploy/api", "deploy/edge"} {
		m := next(t, messages, "the replay of "+want)
		if m.event != "report" {
			t.Fatalf("the replayed message is event %q, want report", m.event)
		}
		if got := workloadIn(t, m.data); got != want {
			t.Errorf("the replay names %s, want %s: the history is replayed oldest first", got, want)
		}
	}

	// Reading the replay proves the browser has joined, so what is published
	// now can only reach it through the stream.
	s.publish(reported("web"))

	m := next(t, messages, "the Report published after connecting")
	if got := workloadIn(t, m.data); got != "deploy/web" {
		t.Errorf("the stream delivered %s, want the Report published after connecting", got)
	}
}

// A still cluster must not look like a dead server, so a stream with nothing
// to say says so on a clock of its own.
func TestEventsKeepAlive(t *testing.T) {
	quickly(t, 20*time.Millisecond)

	s, url := serving(t, Options{Source: Stream(make(chan *collect.Result))})
	s.publish(reported("api"))

	messages := listen(t, url)
	if m := next(t, messages, "the replay"); m.event != "report" {
		t.Fatalf("the first message is event %q, want the replayed report", m.event)
	}

	m := next(t, messages, "a keepalive")
	if m.comment != "keepalive" {
		t.Errorf("the stream sent %+v, want a keepalive comment", m)
	}
	if m.event != "" || m.data != "" {
		t.Errorf("the keepalive carries %+v, want a comment a browser ignores", m)
	}
}

// quickly shrinks the keepalive, because a test should not wait out a quarter
// of a minute of stillness to see one.
func quickly(t *testing.T, interval time.Duration) {
	t.Helper()
	was := keepaliveInterval
	keepaliveInterval = interval
	t.Cleanup(func() { keepaliveInterval = was })
}

// A browser that cannot keep up is dropped. Waiting for one tab would stall
// the watch for every other, and what it missed is a state the cluster has
// already left behind.
func TestSlowBrowserIsDropped(t *testing.T) {
	s := newServer(Options{Source: Stream(make(chan *collect.Result))})

	// A browser with no room left for the next Report is a browser that has
	// fallen behind, which is what an unbuffered channel is here.
	c := &client{updates: make(chan []byte)}
	s.clients[c] = struct{}{}

	s.publish(reported("api"))

	if _, open := <-c.updates; open {
		t.Error("the stream sent to a browser that could not take it")
	}
	if len(s.clients) != 0 {
		t.Errorf("%d browsers are still registered, want the slow one forgotten", len(s.clients))
	}
	// Dropping one browser must not cost the run the Report itself.
	if len(s.ring.entries) != 1 {
		t.Errorf("the ring holds %d Reports, want the one published", len(s.ring.entries))
	}
}

// /api/meta is what the browser needs before the first Report: which
// Inspection to open, and whether to expect a second one at all.
func TestMeta(t *testing.T) {
	tests := []struct {
		name    string
		opts    Options
		initial string
		static  bool
	}{
		{
			name:   "a watched cluster keeps changing",
			opts:   Options{Source: Stream(make(chan *collect.Result))},
			static: false,
		},
		{
			name:   "a run from -f never changes",
			opts:   Options{Source: Snapshot(reported("api"))},
			static: true,
		},
		{
			name:    "a positional names the Inspection to open",
			opts:    Options{Initial: "deploy/api", Source: Stream(make(chan *collect.Result))},
			initial: "deploy/api",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, url := serving(t, tt.opts)
			started := time.Now()

			_, body := get(t, url+"/api/meta")
			var got meta
			if err := json.Unmarshal([]byte(body), &got); err != nil {
				t.Fatalf("the metadata does not parse: %v\n%s", err, body)
			}

			if got.Static != tt.static {
				t.Errorf("static = %v, want %v", got.Static, tt.static)
			}
			if got.Initial != tt.initial {
				t.Errorf("initial = %q, want %q", got.Initial, tt.initial)
			}
			// The observed Timeline begins when the run did, and nothing
			// before that was seen by this server.
			if got.StartedAt.After(started) || started.Sub(got.StartedAt) > time.Minute {
				t.Errorf("startedAt = %s, want the moment this run started (%s)", got.StartedAt, started)
			}
		})
	}
}

// The Dashboard is served out of the binary under a policy strict enough to
// forbid inline script, which is why the page keeps its script in a file.
func TestAssetsAreServedUnderAStrictPolicy(t *testing.T) {
	_, url := serving(t, Options{Source: Snapshot(reported("api"))})

	resp, page := get(t, url+"/")
	if got := resp.Header.Get("Content-Security-Policy"); got != "default-src 'self'" {
		t.Errorf("the policy is %q, want default-src 'self'", got)
	}
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options is %q, want nosniff", got)
	}
	if !strings.Contains(page, `src="app.js"`) {
		t.Errorf("the page does not load the script the policy makes it keep in a file:\n%s", page)
	}
	if strings.Contains(page, "<script>") {
		t.Errorf("the page carries inline script, which the policy forbids:\n%s", page)
	}
	// Style is forbidden inline for the same reason script is: style-src has
	// nothing of its own to fall back on but default-src.
	if !strings.Contains(page, `href="app.css"`) {
		t.Errorf("the page does not load the style the policy makes it keep in a file:\n%s", page)
	}
	if strings.Contains(page, "<style>") {
		t.Errorf("the page carries inline style, which the policy forbids:\n%s", page)
	}

	// The script is embedded alongside the page, or the page is on its own in
	// the archive that ships.
	if _, script := get(t, url+"/app.js"); !strings.Contains(script, "EventSource") {
		t.Errorf("the script does not connect to the stream:\n%s", script)
	}
	// Every response carries the policy, not only the one the page is on.
	if resp, _ := get(t, url+"/api/meta"); resp.Header.Get("Content-Security-Policy") == "" {
		t.Error("the metadata is served without a policy")
	}
}

// contentTypes is what a browser insists on for each of the files the page
// loads. nosniff is set on every response, so a stylesheet served as plain
// text is a page with no style, and a module served as anything but
// JavaScript is a page that does nothing at all.
var contentTypes = map[string]string{
	".css": "text/css",
	".js":  "text/javascript",
	".svg": "image/svg+xml",
}

// What the page loads, and what a module imports. Both are this repo's own
// files rather than anything a cluster wrote, so they are matched rather than
// parsed.
var (
	referenced = regexp.MustCompile(`(?:src|href)="([^"]+)"`)
	imported   = regexp.MustCompile(`(?m)^import[^"\']*["\']\./([^"\']+)["\']`)
	queried    = regexp.MustCompile(`querySelector\("#([^"]+)"\)`)
)

// The Dashboard is one binary, which means every file the page pulls in is
// embedded in it: the style, the icon and the script the page names, and the
// modules that script imports. A file that is not is a page that half loads
// on the machine the plugin was installed on rather than the one it was
// built on.
func TestEveryAssetThePageLoadsIsServed(t *testing.T) {
	_, url := serving(t, Options{Source: Snapshot(reported("api"))})
	_, page := get(t, url+"/")

	pending := loads(page)
	if len(pending) == 0 {
		t.Fatalf("the page loads nothing at all:\n%s", page)
	}

	seen := map[string]bool{}
	for len(pending) > 0 {
		asset := pending[0]
		pending = pending[1:]
		if seen[asset] {
			continue
		}
		seen[asset] = true

		// The Dashboard talks to itself and to nothing else. A page that
		// reaches off loopback has told somebody else what is in the
		// cluster, and the policy would have refused it anyway.
		if strings.Contains(asset, "://") || strings.HasPrefix(asset, "/") {
			t.Errorf("the page loads %s from somewhere that is not the plugin", asset)
			continue
		}
		want, known := contentTypes[path.Ext(asset)]
		if !known {
			t.Errorf("the page loads %s, which is not a style, an icon or a script", asset)
			continue
		}

		resp, body := get(t, url+"/"+asset)
		if got, _, _ := strings.Cut(resp.Header.Get("Content-Type"), ";"); got != want {
			t.Errorf("%s is served as %q, want %s", asset, got, want)
		}
		if strings.TrimSpace(body) == "" {
			t.Errorf("%s is served empty", asset)
		}
		pending = append(pending, imports(body)...)
	}
}

// The Inspection finds its own parts inside the panel rather than being handed
// them, because it is one section with a dozen of them. That makes an id
// renamed in the markup and not in the script a panel that half draws, in the
// browser, where no Go test would be looking. Matching the two here is what
// notices.
func TestThePageCarriesEveryElementTheScriptLooksFor(t *testing.T) {
	_, url := serving(t, Options{Source: Snapshot(reported("api"))})
	_, page := get(t, url+"/")

	for _, module := range []string{"app.js", "inspection.js"} {
		_, body := get(t, url+"/"+module)
		wanted := queried.FindAllStringSubmatch(body, -1)
		if len(wanted) == 0 {
			t.Errorf("%s looks nothing up in the page", module)
		}
		for _, match := range wanted {
			if !strings.Contains(page, `id="`+match[1]+`"`) {
				t.Errorf("%s looks for #%s, which the page does not carry", module, match[1])
			}
		}
	}
}

// loads is every file the page names, in the order it names them.
func loads(page string) []string {
	var assets []string
	for _, match := range referenced.FindAllStringSubmatch(page, -1) {
		assets = append(assets, match[1])
	}
	return assets
}

// imports is every module a module pulls in beside itself.
func imports(module string) []string {
	var assets []string
	for _, match := range imported.FindAllStringSubmatch(module, -1) {
		assets = append(assets, match[1])
	}
	return assets
}

// Run is the whole of --serve: bind, say where, serve until the signal, and
// exit as if nothing had happened.
func TestRunServesUntilCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	results := make(chan *collect.Result, 1)
	results <- reported("api")

	var announced recorder
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			Addr:    "127.0.0.1:0",
			Initial: "deploy/api",
			Source:  Stream(results),
			Out:     &announced,
		})
	}()

	url := awaitURL(t, &announced)
	if !strings.Contains(announced.String(), "Opening on deploy/api") {
		t.Errorf("the announcement is %q, want it to name the Inspection it opens on", announced.String())
	}

	// The first Report is published before the URL is, so there is never a
	// moment where the address answers and the Report does not.
	if _, body := get(t, url+"/api/report"); workloadIn(t, body) != "deploy/api" {
		t.Errorf("the first Report is not the one the watch handed over:\n%s", body)
	}

	messages := listen(t, url)
	next(t, messages, "the replay of the first Report")

	results <- reported("edge")
	if m := next(t, messages, "the Report the cluster changed into"); workloadIn(t, m.data) != "deploy/edge" {
		t.Errorf("the stream carries %s, want the Result the watch pushed", m.data)
	}

	// What a signal comes to. Nothing about it is a failure, so nothing about
	// it is an error.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("a cancelled run returned %v, want it to exit as if nothing had happened", err)
		}
	case <-time.After(arrives):
		t.Fatal("the run did not shut down after its context was cancelled")
	}
}

// recorder is where a run under test announces itself. It is read from the
// test while the run writes to it, which is the whole reason it is not a
// bytes.Buffer.
type recorder struct {
	mu   sync.Mutex
	said strings.Builder
}

func (r *recorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.said.Write(p)
}

func (r *recorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.said.String()
}

// awaitURL reads the address out of what the run announced, waiting for it to
// be announced at all.
func awaitURL(t *testing.T, announced *recorder) string {
	t.Helper()
	deadline := time.Now().Add(arrives)
	for time.Now().Before(deadline) {
		line, _, found := strings.Cut(announced.String(), "\n")
		if found && strings.HasPrefix(line, "Serving on ") {
			return strings.TrimPrefix(line, "Serving on ")
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the run never said where it was serving, only %q", announced.String())
	return ""
}
