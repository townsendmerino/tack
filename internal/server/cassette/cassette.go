// Package cassette records a model server's HTTP traffic and replays it, so the loop's tests never need a live model
// (DESIGN §15.1, tier 0). A cassette is a JSON file of interactions in order. Replay is strict: each request must
// match the next recorded one (method, path and JSON body, compared as JSON), so a change in what tack sends fails
// the test that replays it instead of silently getting a recorded answer to a different question.
package cassette

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
)

// Cassette is the file format.
type Cassette struct {
	// Note says where the recording came from: server, model, flags, date. Free text for the reader.
	Note         string        `json:"note"`
	Interactions []Interaction `json:"interactions"`
}

// Interaction is one request and its response.
type Interaction struct {
	Request  Req  `json:"request"`
	Response Resp `json:"response"`
}

// Req is a recorded request. Body is the JSON body, or null; auth headers are never recorded.
type Req struct {
	Method string          `json:"method"`
	Path   string          `json:"path"`
	Body   json.RawMessage `json:"body"`
}

// Resp is a recorded response. Body is the raw body text: JSON, or an SSE stream.
type Resp struct {
	Status      int    `json:"status"`
	ContentType string `json:"content_type"`
	RetryAfter  string `json:"retry_after,omitempty"`
	Body        string `json:"body"`
}

// Load reads a cassette file.
func Load(path string) (*Cassette, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Cassette
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("cassette %s: %w", path, err)
	}
	return &c, nil
}

// Save writes a cassette file, indented for review.
func (c *Cassette) Save(path string) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// Replayer is an http.RoundTripper that answers from a cassette in order.
type Replayer struct {
	mu   sync.Mutex
	c    *Cassette
	next int
	// Errs collects mismatches, so a test can report them even when the client swallowed the transport error.
	Errs []error
}

// NewReplayer replays c.
func NewReplayer(c *Cassette) *Replayer { return &Replayer{c: c} }

// Client returns an *http.Client that uses the replayer.
func (r *Replayer) Client() *http.Client { return &http.Client{Transport: r} }

// RoundTrip implements http.RoundTripper.
func (r *Replayer) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	got, err := capture(req)
	if err != nil {
		return nil, err
	}
	if r.next >= len(r.c.Interactions) {
		err := fmt.Errorf("cassette: unexpected request %d (%s %s): the cassette has %d", r.next+1, got.Method, got.Path, len(r.c.Interactions))
		r.Errs = append(r.Errs, err)
		return nil, err
	}
	want := r.c.Interactions[r.next]
	if d := diff(want.Request, got); d != "" {
		err := fmt.Errorf("cassette: request %d differs from the recording: %s", r.next+1, d)
		r.Errs = append(r.Errs, err)
		return nil, err
	}
	r.next++
	return toHTTP(want.Response, req), nil
}

// Done reports whether every recorded interaction was replayed, and any mismatch seen.
func (r *Replayer) Done() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.Errs) > 0 {
		return r.Errs[0]
	}
	if r.next != len(r.c.Interactions) {
		return fmt.Errorf("cassette: %d of %d interactions replayed", r.next, len(r.c.Interactions))
	}
	return nil
}

// Recorder is an http.RoundTripper that forwards to a live server and records each exchange.
type Recorder struct {
	mu   sync.Mutex
	next http.RoundTripper
	C    Cassette
}

// NewRecorder records traffic sent through next (nil means http.DefaultTransport).
func NewRecorder(note string, next http.RoundTripper) *Recorder {
	if next == nil {
		next = http.DefaultTransport
	}
	return &Recorder{next: next, C: Cassette{Note: note}}
}

// Client returns an *http.Client that uses the recorder.
func (r *Recorder) Client() *http.Client { return &http.Client{Transport: r} }

// RoundTrip implements http.RoundTripper. The response body is read in full before it is returned.
func (r *Recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	got, err := capture(req)
	if err != nil {
		return nil, err
	}
	resp, err := r.next.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	rec := Resp{Status: resp.StatusCode, ContentType: resp.Header.Get("Content-Type"),
		RetryAfter: resp.Header.Get("Retry-After"), Body: string(body)}
	r.mu.Lock()
	r.C.Interactions = append(r.C.Interactions, Interaction{Request: got, Response: rec})
	r.mu.Unlock()
	return toHTTP(rec, req), nil
}

// capture reads req's body (restoring it) and returns the recordable request.
func capture(req *http.Request) (Req, error) {
	out := Req{Method: req.Method, Path: req.URL.Path, Body: json.RawMessage("null")}
	if req.Body == nil {
		return out, nil
	}
	b, err := io.ReadAll(req.Body)
	req.Body.Close()
	if err != nil {
		return out, err
	}
	req.Body = io.NopCloser(bytes.NewReader(b))
	if len(bytes.TrimSpace(b)) > 0 {
		if !json.Valid(b) {
			return out, fmt.Errorf("cassette: request body is not JSON")
		}
		out.Body = b
	}
	return out, nil
}

func toHTTP(r Resp, req *http.Request) *http.Response {
	h := http.Header{}
	if r.ContentType != "" {
		h.Set("Content-Type", r.ContentType)
	}
	if r.RetryAfter != "" {
		h.Set("Retry-After", r.RetryAfter)
	}
	return &http.Response{StatusCode: r.Status, Status: http.StatusText(r.Status), Header: h, Request: req,
		Body: io.NopCloser(strings.NewReader(r.Body)), ContentLength: int64(len(r.Body)), Proto: "HTTP/1.1",
		ProtoMajor: 1, ProtoMinor: 1}
}

// diff describes the first difference between a recorded and an actual request, or returns "".
func diff(want, got Req) string {
	if want.Method != got.Method || want.Path != got.Path {
		return fmt.Sprintf("want %s %s, got %s %s", want.Method, want.Path, got.Method, got.Path)
	}
	var w, g any
	_ = json.Unmarshal(want.Body, &w)
	_ = json.Unmarshal(got.Body, &g)
	if p := firstDiff("body", w, g); p != "" {
		return p
	}
	return ""
}

func firstDiff(path string, w, g any) string {
	if reflect.DeepEqual(w, g) {
		return ""
	}
	switch wv := w.(type) {
	case map[string]any:
		gv, ok := g.(map[string]any)
		if !ok {
			break
		}
		keys := map[string]bool{}
		for k := range wv {
			keys[k] = true
		}
		for k := range gv {
			keys[k] = true
		}
		var ks []string
		for k := range keys {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		for _, k := range ks {
			if d := firstDiff(path+"."+k, wv[k], gv[k]); d != "" {
				return d
			}
		}
	case []any:
		gv, ok := g.([]any)
		if !ok {
			break
		}
		for i := 0; i < len(wv) && i < len(gv); i++ {
			if d := firstDiff(fmt.Sprintf("%s[%d]", path, i), wv[i], gv[i]); d != "" {
				return d
			}
		}
		if len(wv) != len(gv) {
			return fmt.Sprintf("%s: recorded %d elements, sent %d", path, len(wv), len(gv))
		}
	}
	wb, _ := json.Marshal(w)
	gb, _ := json.Marshal(g)
	return fmt.Sprintf("%s: recorded %s, sent %s", path, trunc(wb), trunc(gb))
}

func trunc(b []byte) string {
	if len(b) > 200 {
		return string(b[:200]) + "…"
	}
	return string(b)
}
