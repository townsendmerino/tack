package models

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/townsendmerino/tack/internal/config"
	"github.com/townsendmerino/tack/internal/server/openai"
)

// TestOpenMakesAnOpenAISource: an openai-remote server is a source over the OpenAI-compatible client, which serves
// the configured model with the window /v1/models lists.
func TestOpenMakesAnOpenAISource(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, `{"object":"list","data":[{"id":"qwen","context_window":32768},{"id":"other"}]}`)
	}))
	t.Cleanup(ts.Close)
	cfg := &config.Config{Servers: map[string]config.Server{"box": {Kind: config.KindOpenAI, URL: ts.URL, Model: "qwen"}}}
	src, err := Open(cfg, "box", t.TempDir(), ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	srv, err := src.Ensure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := srv.(*openai.Client); !ok || srv.Name() != "openai@box" || src.Model() != "qwen" || src.Window() != 32768 {
		t.Errorf("server %T named %q, model %q, window %d", srv, srv.Name(), src.Model(), src.Window())
	}
}
