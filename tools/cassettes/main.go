// Command cassettes records the goinfer client's cassettes (internal/server/goinfer/testdata) against a live
// goinfer-serve, running the scenarios in internal/server/goinfer/goinfertest. It is a tier-1 tool, run by hand when the
// client's wire format changes; the tests replay what it wrote and never need a model.
//
//	go run ./tools/cassettes -url http://127.0.0.1:18080 -note "goinfer <rev>, <model>, <flags>"
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/townsendmerino/tack/internal/server/cassette"
	"github.com/townsendmerino/tack/internal/server/goinfer"
	"github.com/townsendmerino/tack/internal/server/goinfer/goinfertest"
)

func main() {
	url := flag.String("url", "", "goinfer-serve base URL")
	key := flag.String("key", "", "API key, if the server has one")
	note := flag.String("note", "", "where the recording came from (server revision, model, flags)")
	out := flag.String("out", "internal/server/goinfer/testdata", "directory for the cassettes")
	flag.Parse()
	if *url == "" || *note == "" {
		fmt.Fprintln(os.Stderr, "cassettes: -url and -note are required")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	date := time.Now().Format("2006-01-02")

	record := func(name string, run func(c *goinfer.Client) error) {
		rec := cassette.NewRecorder(*note+"; recorded "+date, nil)
		c := goinfer.New("goinfer@local", *url, *key, rec.Client())
		if err := run(c); err != nil {
			fmt.Fprintf(os.Stderr, "cassettes: %s: %v\n", name, err)
			os.Exit(1)
		}
		p := filepath.Join(*out, name+".json")
		if err := rec.C.Save(p); err != nil {
			fmt.Fprintln(os.Stderr, "cassettes:", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "cassettes: wrote %s (%d interactions)\n", p, len(rec.C.Interactions))
	}

	var model string
	record("models", func(c *goinfer.Client) error {
		ms, err := c.Models(ctx)
		if err == nil && len(ms) > 0 {
			model = ms[0].ID
		}
		if model == "" {
			return fmt.Errorf("no models served (%v)", err)
		}
		return nil
	})
	record("text", func(c *goinfer.Client) error {
		_, err := goinfertest.Text(ctx, c, model, nil)
		return err
	})
	record("tools", func(c *goinfer.Client) error {
		_, err := goinfertest.ToolLoop(ctx, c, model)
		return err
	})
	record("count_tokens", func(c *goinfer.Client) error {
		n, err := c.CountTokens(ctx, goinfertest.ToolConversation(model).Request())
		fmt.Fprintf(os.Stderr, "cassettes: count_tokens = %d\n", n)
		return err
	})
}
