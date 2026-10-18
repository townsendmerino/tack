package pilot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Sampling is what opencode sends with every request: Qwen2.5's own recommended settings (pilot pre-registration,
// amendment 6). Without them goinfer samples at temperature 1.0 over the whole vocabulary, which is what broke the
// first run's tool calls. They go in each model's options, which the @ai-sdk/openai-compatible provider spreads into
// the request body as written; CheckSampling confirms they arrive.
var Sampling = map[string]any{"temperature": 0.7, "top_p": 0.8, "top_k": 20}

// Capture is a recording reverse proxy in front of goinfer: every request opencode sends and every response, raw (the
// SSE stream as streamed), goes to the current run's directory as NNNN.req.json and NNNN.resp, with NNNN.meta holding
// the method, path, status and the client-side timings (CaptureTiming). Headers are not recorded, so the API key is not. It listens on loopback only, and the
// bodies stay in the run's local directory (owner, 2026-10-17: goinfer may replay first requests).
type Capture struct {
	URL string // what opencode is pointed at

	ln  net.Listener
	srv *http.Server

	mu  sync.Mutex
	dir string
	n   int
}

// StartCapture starts the proxy in front of upstream, a goinfer base URL.
func StartCapture(upstream string) (*Capture, error) {
	u, err := url.Parse(upstream)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	c := &Capture{URL: "http://" + ln.Addr().String(), ln: ln}
	rp := httputil.NewSingleHostReverseProxy(u)
	rp.FlushInterval = -1 // stream SSE as it arrives
	rp.ModifyResponse = func(resp *http.Response) error {
		if f, ok := resp.Request.Context().Value(captureKey{}).(*os.File); ok && f != nil {
			resp.Body = &teeBody{ReadCloser: resp.Body, w: f}
		}
		return nil
	}
	c.srv = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		resp, meta := c.open(r, body)
		if resp != nil {
			r = r.WithContext(contextWith(r, resp))
			defer resp.Close()
		}
		sw := &statusWriter{ResponseWriter: w, status: 200}
		t0 := time.Now()
		sw.start = t0
		rp.ServeHTTP(sw, r)
		total := time.Since(t0)
		if meta != "" {
			_ = os.WriteFile(meta, fmt.Appendf(nil, "%s %s %d ttfb_ms=%d total_ms=%d\n", r.Method, r.URL.Path, sw.status,
				sw.ttfb.Milliseconds(), total.Milliseconds()), 0o644)
		}
	})}
	go c.srv.Serve(ln)
	return c, nil
}

// SetDir starts a new run's recording in dir, numbering from 1; "" stops recording.
func (c *Capture) SetDir(dir string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dir, c.n = dir, 0
	if dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
}

// Count is how many requests the current run's recording holds.
func (c *Capture) Count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// Close stops the proxy.
func (c *Capture) Close() error { return c.srv.Close() }

// open writes a request's body and returns the file its response body is teed into, and the meta path.
func (c *Capture) open(r *http.Request, body []byte) (*os.File, string) {
	c.mu.Lock()
	if c.dir == "" {
		c.mu.Unlock()
		return nil, ""
	}
	c.n++
	base := filepath.Join(c.dir, fmt.Sprintf("%04d", c.n))
	c.mu.Unlock()
	_ = os.WriteFile(base+".req.json", body, 0o644)
	f, err := os.Create(base + ".resp")
	if err != nil {
		return nil, base + ".meta"
	}
	return f, base + ".meta"
}

type captureKey struct{}

func contextWith(r *http.Request, f *os.File) context.Context {
	return context.WithValue(r.Context(), captureKey{}, f)
}

// teeBody copies a response body into the recording as the proxy reads it.
type teeBody struct {
	io.ReadCloser
	w io.Writer
}

func (t *teeBody) Read(p []byte) (int, error) {
	n, err := t.ReadCloser.Read(p)
	if n > 0 {
		_, _ = t.w.Write(p[:n])
	}
	return n, err
}

// statusWriter remembers the status and passes flushes through, so the stream reaches opencode as it is produced.
type statusWriter struct {
	http.ResponseWriter
	status int
	start  time.Time
	ttfb   time.Duration // to the first byte of the response body
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.ttfb == 0 && len(b) > 0 {
		s.ttfb = time.Since(s.start)
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// CheckSampling reads a run's first recorded model request (firstModelRequest) and reports whether it carries every Sampling field with its
// value (pilot pre-registration, amendment 6: the check that top_k, which the provider would otherwise drop, reaches
// goinfer).
func CheckSampling(dir string) error {
	b, err := os.ReadFile(firstModelRequest(dir))
	if err != nil {
		return fmt.Errorf("pilot: no first request recorded in %s: %w", dir, err)
	}
	var body map[string]any
	if err := json.Unmarshal(b, &body); err != nil {
		return fmt.Errorf("pilot: the first request is not JSON: %w", err)
	}
	for k, want := range Sampling {
		got, ok := body[k]
		if !ok {
			return fmt.Errorf("pilot: the first request does not carry %s: opencode did not pass it to goinfer", k)
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			return fmt.Errorf("pilot: the first request carries %s=%v, want %v", k, got, want)
		}
	}
	return nil
}

// CaptureTiming is one recorded request's client-side timing, read from its NNNN.meta: the milliseconds to the first
// byte of the response body and to its end, as the proxy saw them. Beside goinfer's own TTFT and total for the same
// request, the difference is the time spent between the client and the server (the tailnet, for A1).
type CaptureTiming struct {
	TTFBms, TotalMs int
}

// ReadTiming reads a recorded request's meta file; ok is false for a file without timings.
func ReadTiming(meta string) (CaptureTiming, bool) {
	b, err := os.ReadFile(meta)
	if err != nil {
		return CaptureTiming{}, false
	}
	var t CaptureTiming
	var method, path string
	var status int
	if n, _ := fmt.Sscanf(string(b), "%s %s %d ttfb_ms=%d total_ms=%d", &method, &path, &status, &t.TTFBms, &t.TotalMs); n != 5 {
		return CaptureTiming{}, false
	}
	return t, true
}

// firstModelRequest is the body of the first recorded request that asked the model for a reply (a POST to /v1/messages
// or /v1/chat/completions), skipping a client's model listing. A recording without metas falls back to 0001.
func firstModelRequest(dir string) string {
	metas, _ := filepath.Glob(filepath.Join(dir, "*.meta"))
	sort.Strings(metas)
	for _, m := range metas {
		b, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		f := strings.Fields(string(b))
		if len(f) >= 2 && f[0] == "POST" && (f[1] == "/v1/messages" || f[1] == "/v1/chat/completions") {
			return strings.TrimSuffix(m, ".meta") + ".req.json"
		}
	}
	return filepath.Join(dir, "0001.req.json")
}
