// SPDX-License-Identifier: Apache-2.0

// Package aitest is the conformance suite of AI providers ([ai.Provider]):
// each driver module runs it, so every provider behaves alike for the
// ai package: text, streaming, conversations, tool calls, structured
// output, the token limit and errors.
//
//	func TestConformance(t *testing.T) {
//		aitest.Run(t, aitest.Config{
//			Name:   "anthropic",
//			Model:  "claude-haiku-4-5",
//			KeyEnv: "ANTHROPIC_API_KEY",
//			New: func(t *testing.T, hc *http.Client, key string) ai.Provider {
//				return anthropic.New(key, option.WithHTTPClient(hc))
//			},
//		})
//	}
//
// The suite runs against recordings, cassettes in testdata/aitest: each
// test's HTTP requests and the provider's responses. A test checks that
// the requests it sends match the recorded ones, and the provider's
// handling of the recorded answers. Environment variables change that:
//
//   - ANETOS_AI_RECORD=1 sends the requests to the provider (its API key
//     in KeyEnv) and records the cassettes again; never the headers,
//     which hold the key, and not a failed test's, whose answers may be
//     errors.
//   - ANETOS_AI_LIVE=1 sends them, records nothing.
//   - ANETOS_AI_UPDATE_REQUESTS=1 replays the answers and rewrites the
//     recorded requests: after a deliberate change to what the driver
//     sends, when there's no key to record with.
//
// ANETOS_TEST_<NAME>_MODEL (ANETOS_TEST_ANTHROPIC_MODEL…) chooses the
// model for recording and live runs; replays use Config.Model, the
// recordings'. Headers aren't compared, only methods, paths and bodies.
package aitest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"anetos.dev/anetos/ai"
)

// Config describes the provider under test.
type Config struct {
	// Name is the provider's name ("anthropic"): what Provider.Name
	// returns, and ANETOS_TEST_<NAME>_MODEL's.
	Name string
	// Model is the model of the recordings.
	Model string
	// KeyEnv is the environment variable holding the provider's API key,
	// for recording and live runs ("ANTHROPIC_API_KEY"); empty if it
	// needs none.
	KeyEnv string
	// NeedsEnv are other environment variables recording and live runs
	// need (a server's URL); without them, the suite is skipped.
	NeedsEnv []string
	// New returns the provider under test: it sends its HTTP requests
	// with hc, and authenticates with key (a placeholder when replaying).
	// It must not retry failed requests.
	New func(t *testing.T, hc *http.Client, key string) ai.Provider
	// Dir holds the cassettes; default testdata/aitest.
	Dir string
	// Skip names the tests the provider can't pass, with why.
	Skip map[string]string
}

type mode int

const (
	replay mode = iota
	record
	live
	updateRequests
)

// Replaying reports whether the suite replays recordings, as it does
// unless ANETOS_AI_RECORD or ANETOS_AI_LIVE is set: then a provider
// under test must not use the environment's settings.
func Replaying() bool { m := modeOf(); return m == replay || m == updateRequests }

func modeOf() mode {
	switch {
	case os.Getenv("ANETOS_AI_RECORD") == "1":
		return record
	case os.Getenv("ANETOS_AI_LIVE") == "1":
		return live
	case os.Getenv("ANETOS_AI_UPDATE_REQUESTS") == "1":
		return updateRequests
	}
	return replay
}

// Run runs the conformance suite against the provider c.New makes.
func Run(t *testing.T, c Config) {
	if c.New == nil || c.Name == "" || c.Model == "" {
		t.Fatal("aitest: Config needs Name, Model and New")
	}
	if c.Dir == "" {
		c.Dir = filepath.Join("testdata", "aitest")
	}
	m := modeOf()
	model := c.Model
	key := "test-key"
	if m == record || m == live {
		if env := "ANETOS_TEST_" + strings.ToUpper(strings.ReplaceAll(c.Name, "-", "_")) + "_MODEL"; os.Getenv(env) != "" {
			model = os.Getenv(env)
		}
		if c.KeyEnv != "" {
			if key = os.Getenv(c.KeyEnv); key == "" {
				t.Skipf("aitest: %s isn't set: can't call %s", c.KeyEnv, c.Name)
			}
		}
		for _, env := range c.NeedsEnv {
			if os.Getenv(env) == "" {
				t.Skipf("aitest: %s isn't set: can't call %s", env, c.Name)
			}
		}
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if why, ok := c.Skip[tc.name]; ok {
				t.Skip(why)
			}
			hc := &http.Client{Transport: newTransport(t, m, filepath.Join(c.Dir, tc.name+".json"))}
			p := c.New(t, hc, key)
			if p.Name() != c.Name {
				t.Errorf("Name() = %q, want %q", p.Name(), c.Name)
			}
			tc.run(t, p, model)
		})
	}
}

// cassette is a test's recorded HTTP exchanges.
type cassette struct {
	Interactions []interaction `json:"interactions"`
}

type interaction struct {
	Request  recordedRequest  `json:"request"`
	Response recordedResponse `json:"response"`
}

type recordedRequest struct {
	Method string          `json:"method"`
	Path   string          `json:"path"`
	Body   json.RawMessage `json:"body,omitempty"`
}

type recordedResponse struct {
	Status      int    `json:"status"`
	ContentType string `json:"content_type,omitempty"`
	Body        string `json:"body"`
}

// transport records or replays a test's HTTP exchanges.
type transport struct {
	t       *testing.T
	fail    func(format string, args ...any) // t.Errorf
	mode    mode
	path    string
	mu      sync.Mutex
	tape    cassette
	next    int
	changed bool
}

func newTransport(t *testing.T, m mode, path string) *transport {
	tr := &transport{t: t, fail: t.Errorf, mode: m, path: path}
	if m == replay || m == updateRequests {
		tr.tape = loadTape(t, path)
	}
	t.Cleanup(tr.save)
	return tr
}

// loadTape reads the cassette at path.
func loadTape(t *testing.T, path string) cassette {
	var tape cassette
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("aitest: no recording for %s (%v): record it with the provider's key and ANETOS_AI_RECORD=1", t.Name(), err)
	}
	if err := json.Unmarshal(data, &tape); err != nil {
		t.Fatalf("aitest: %s: %v", path, err)
	}
	for i := range tape.Interactions { // indented in the file
		if body := tape.Interactions[i].Request.Body; len(body) > 0 {
			c, err := canonicalJSON(body)
			if err != nil {
				t.Fatalf("aitest: %s: request %d: %v", path, i+1, err)
			}
			tape.Interactions[i].Request.Body = c
		}
	}
	return tape
}

// requestOf is r's recorded form: its method, path and query (without
// a key), and body, its JSON keys sorted.
func requestOf(r *http.Request) (recordedRequest, []byte, error) {
	var body []byte
	if r.Body != nil {
		var err error
		if body, err = io.ReadAll(r.Body); err != nil {
			return recordedRequest{}, nil, err
		}
		_ = r.Body.Close()
	}
	q := r.URL.Query()
	q.Del("key")
	path := r.URL.Path
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	rr := recordedRequest{Method: r.Method, Path: path}
	if len(body) > 0 {
		canonical, err := canonicalJSON(body)
		if err != nil {
			return recordedRequest{}, nil, fmt.Errorf("the request's body isn't JSON: %w", err)
		}
		rr.Body = canonical
	}
	return rr, body, nil
}

func canonicalJSON(data []byte) (json.RawMessage, error) {
	var v any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

func (tr *transport) RoundTrip(r *http.Request) (*http.Response, error) {
	rr, body, err := requestOf(r)
	if err != nil {
		return nil, err
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	switch tr.mode {
	case record, live:
		r2 := r.Clone(r.Context())
		r2.Body = io.NopCloser(bytes.NewReader(body))
		r2.ContentLength = int64(len(body))
		resp, err := http.DefaultTransport.RoundTrip(r2)
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if tr.mode == record {
			tr.tape.Interactions = append(tr.tape.Interactions, interaction{rr, recordedResponse{
				Status: resp.StatusCode, ContentType: resp.Header.Get("Content-Type"), Body: string(data),
			}})
			tr.changed = true
		}
		resp.Body = io.NopCloser(bytes.NewReader(data))
		return resp, nil
	}
	if tr.next >= len(tr.tape.Interactions) {
		tr.fail("aitest: request %d (%s %s) isn't in the recording %s", tr.next+1, rr.Method, rr.Path, tr.path)
		return nil, errors.New("aitest: no recorded response")
	}
	in := &tr.tape.Interactions[tr.next]
	tr.next++
	if in.Request.Method != rr.Method || in.Request.Path != rr.Path || !bytes.Equal(in.Request.Body, rr.Body) {
		if tr.mode == updateRequests {
			in.Request = rr
			tr.changed = true
		} else {
			tr.fail("aitest: request %d differs from the recording %s (ANETOS_AI_UPDATE_REQUESTS=1 accepts the new one):\nsent:     %s %s %s\nrecorded: %s %s %s",
				tr.next, tr.path, rr.Method, rr.Path, rr.Body, in.Request.Method, in.Request.Path, in.Request.Body)
		}
	}
	h := http.Header{}
	if in.Response.ContentType != "" {
		h.Set("Content-Type", in.Response.ContentType)
	}
	return &http.Response{
		StatusCode:    in.Response.Status,
		Status:        fmt.Sprintf("%d %s", in.Response.Status, http.StatusText(in.Response.Status)),
		Header:        h,
		Body:          io.NopCloser(strings.NewReader(in.Response.Body)),
		ContentLength: int64(len(in.Response.Body)),
		Request:       r,
		Proto:         "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
	}, nil
}

// save writes the cassette if the test recorded or updated it.
func (tr *transport) save() {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if (tr.mode == replay || tr.mode == updateRequests) && tr.next < len(tr.tape.Interactions) && !tr.t.Failed() {
		tr.fail("aitest: %d of the recording's %d requests weren't sent", len(tr.tape.Interactions)-tr.next, len(tr.tape.Interactions))
	}
	if tr.mode == record && len(tr.tape.Interactions) == 0 && !tr.t.Failed() {
		tr.fail("aitest: nothing was recorded: the provider didn't send its requests through the test's HTTP client")
		return
	}
	if !tr.changed {
		return
	}
	if tr.mode == record && tr.t.Failed() {
		// Its answers may be errors (a rate limit, a refused key) that a
		// replay mustn't take for the provider's.
		tr.t.Logf("aitest: %s not saved: the test failed", tr.path)
		return
	}
	data, err := json.MarshalIndent(tr.tape, "", "  ")
	if err == nil {
		err = os.MkdirAll(filepath.Dir(tr.path), 0o755)
	}
	if err == nil {
		err = os.WriteFile(tr.path, append(data, '\n'), 0o644)
	}
	if err != nil {
		tr.fail("aitest: save %s: %v", tr.path, err)
	}
}
