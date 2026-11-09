// Command summarycheck is C12's tier-1 prerequisite (docs/measurements/e1-calibration-preregistration-2026-10-18.md
// §18.2): does a server's thinking-off alias, asked as a live summary asks it, write the seven summary headings? For
// each of the n largest logged sessions under -logs that have a compaction, it takes the cut the live loop would make
// (loop.FirstSummary) and runs the summarizer (loop.Summarize) on it against -server, on the alias the server lists.
//
//	go run ./tools/summarycheck -server URL [-key FILE] [-model ID] -logs DIR [-n 5] [-out FILE]
//
// It sends the summarizer's requests and nothing else, reads the logs, and writes only -out (each reply's first 400
// bytes). The registered rule, read on the share of sessions whose summary kept its headings: at least 4 in 5 is
// VERDICT: PASS (C12 may proceed), 1 in 5 or fewer is FAIL (C12 is held), and anything between is INCONCLUSIVE
// (parked for Francis). Fewer sessions than -n, or no alias, is INCONCLUSIVE.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/townsendmerino/tack/internal/loop"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/server"
	"github.com/townsendmerino/tack/internal/server/goinfer"
	"github.com/townsendmerino/tack/tools/gate"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout)) }

// session is one logged session to summarize.
type session struct {
	path    string
	size    int64
	stretch []proto.Event
}

func run(args []string, w io.Writer) int {
	fl := flag.NewFlagSet("summarycheck", flag.ContinueOnError)
	fl.SetOutput(io.Discard)
	url := fl.String("server", "", "the goinfer server's base URL")
	keyFile := fl.String("key", "", "a file holding the server's API key, if it needs one")
	model := fl.String("model", "", "the model whose alias summarizes (default: the first the server lists)")
	logs := fl.String("logs", "", "a directory of logged sessions (event .jsonl files), searched recursively")
	n := fl.Int("n", 5, "how many sessions to summarize, the largest first")
	out := fl.String("out", "", "a file for each reply's first 400 bytes")
	inconclusive := func(why string) int {
		fmt.Fprintln(w, gate.Verdict(gate.Inconclusive, "summarycheck: "+why))
		return 2
	}
	if err := fl.Parse(args); err != nil || *url == "" || *logs == "" || *n < 1 {
		return inconclusive("usage: -server URL [-key FILE] [-model ID] -logs DIR [-n 5] [-out FILE]")
	}
	key := ""
	if *keyFile != "" {
		b, err := os.ReadFile(*keyFile)
		if err != nil {
			return inconclusive(err.Error())
		}
		key = strings.TrimSpace(string(b))
	}
	sessions, err := find(*logs, *n)
	if err != nil {
		return inconclusive(err.Error())
	}
	if len(sessions) < *n {
		return inconclusive(fmt.Sprintf("%d sessions with a compaction and enough requests under %s; want %d", len(sessions), *logs, *n))
	}
	ctx := context.Background()
	srv := goinfer.New("summarycheck", strings.TrimRight(*url, "/"), key, nil)
	ms, err := srv.Models(ctx)
	if err != nil {
		return inconclusive("listing the server's models: " + err.Error())
	}
	fast := ""
	for _, m := range ms {
		if *model == "" || m.ID == *model {
			fast = m.FastModel
			break
		}
	}
	if fast == "" {
		return inconclusive("the server lists no thinking-off alias: start it with -fast-alias")
	}
	var replies strings.Builder
	kept := 0
	for i, s := range sessions {
		var calls []string
		send := func(ctx context.Context, req server.Request) (*server.Response, error) {
			start := time.Now()
			resp, err := srv.Stream(ctx, req, func(server.Delta) {})
			if err != nil {
				calls = append(calls, fmt.Sprintf("error after %v: %v", time.Since(start).Round(time.Millisecond), err))
				return nil, err
			}
			var text strings.Builder
			for _, b := range resp.Content {
				if b.Type == "text" {
					text.WriteString(b.Text)
				}
			}
			calls = append(calls, fmt.Sprintf("%v, %d output tokens", time.Since(start).Round(time.Millisecond), resp.Usage.OutputTokens))
			fmt.Fprintf(&replies, "== %s, call %d\n%s\n\n", s.path, len(calls), cut(text.String(), 400))
			return resp, nil
		}
		sum := loop.Summarize(ctx, send, fast, nil, s.stretch, "")
		verdict := "headings kept"
		if sum.FellBack {
			verdict = "fell back: " + sum.Reason
		} else {
			kept++
		}
		fmt.Fprintf(w, "%d. %s (%d events): %s; calls: %s\n", i+1, s.path, len(s.stretch), verdict, strings.Join(calls, "; "))
	}
	if *out != "" {
		if err := os.WriteFile(*out, []byte(replies.String()), 0o644); err != nil {
			return inconclusive(err.Error())
		}
	}
	fmt.Fprintln(w, verdictFor(kept, len(sessions), fast))
	switch {
	case kept*5 >= 4*len(sessions):
		return 0
	case kept*5 <= len(sessions):
		return 1
	}
	return 2
}

// verdictFor is the registered rule's reading of kept summaries out of n.
func verdictFor(kept, n int, fast string) string {
	msg := fmt.Sprintf("summarycheck: %d of %d summaries on %s kept their headings", kept, n, fast)
	switch {
	case kept*5 >= 4*n:
		return gate.Verdict(gate.OK, msg+": C12 may proceed")
	case kept*5 <= n:
		return gate.Verdict(gate.Fail, msg+": C12 is held until the summarizer prompt is fixed")
	}
	return gate.Verdict(gate.Inconclusive, msg+": parked, Francis decides")
}

// find is the n largest logs under dir with a compaction and enough requests for a first summary.
func find(dir string, n int) ([]session, error) {
	var all []session
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".jsonl") {
			return err
		}
		evs, compacted, err := read(p)
		if err != nil || !compacted {
			return err
		}
		stretch, ok := loop.FirstSummary(evs)
		if !ok {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		all = append(all, session{path: p, size: fi.Size(), stretch: stretch})
		return nil
	})
	sort.SliceStable(all, func(i, j int) bool { return all[i].size > all[j].size })
	return all[:min(n, len(all))], err
}

// read is a log's events, and whether it has a compaction. A line that is not an event is skipped.
func read(path string) ([]proto.Event, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	var evs []proto.Event
	compacted := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		var ev proto.Event
		if json.Unmarshal(sc.Bytes(), &ev) != nil || ev.Type == "" {
			continue
		}
		compacted = compacted || ev.Type == proto.EvCompaction
		evs = append(evs, ev)
	}
	return evs, compacted, sc.Err()
}

func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
