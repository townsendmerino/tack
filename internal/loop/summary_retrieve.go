package loop

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/townsendmerino/tack/internal/tools"
)

// Re-retrieval after a summary (Config.SummaryRetrieve; DESIGN §7.6, step 3): the summary's open threads, taken
// mechanically from its body and anchors, are searched again, so the code they name comes back as it is now instead
// of as the dumps the summary removed. The hits go in the summary message's last section, after the anchors, where a
// compaction already rewrites the history; the section is in the logged summary word for word, so Rebuild replays it.

// retrieveSearchTimeout bounds each search, as summaryCallTimeout bounds each summarizer call: a search that runs out
// finds nothing.
var retrieveSearchTimeout = 5 * time.Second

const (
	// retrieveQueries is how many open threads are searched.
	retrieveQueries = 8
	// retrieveHits is how many hits each thread asks for.
	retrieveHits = 3
	// retrieveHeading opens the section; bodyOf stops at the anchors before it, so it never reaches the next summary.
	retrieveHeading = "## Current code for the open threads (re-read at compaction)"
)

// openThreads is what a summary leaves open, as search queries: each line under the body's "In progress" and "Next
// steps" headings, then the anchors' symbols, at most retrieveQueries, each once.
func openThreads(body string, a Anchors) []string {
	var qs []string
	add := func(q string) {
		q = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(q), "-*0123456789.) "))
		if q == "" || strings.EqualFold(q, "(none)") || len(qs) == retrieveQueries {
			return
		}
		for _, have := range qs {
			if have == q {
				return
			}
		}
		qs = append(qs, cut(q, 200))
	}
	open := false
	for l := range strings.SplitSeq(body, "\n") {
		if h, ok := strings.CutPrefix(strings.TrimSpace(l), "## "); ok {
			open = strings.EqualFold(h, "In progress") || strings.EqualFold(h, "Next steps")
			continue
		}
		if open {
			add(l)
		}
	}
	for _, s := range a.Symbols {
		add(s)
	}
	return qs
}

// retrieveSection is the summary's re-retrieval section for queries, within budget bytes: each query's hits in order,
// each span once, stopping at the first hit that would pass the budget, so the section is the same for the same index.
// "" when nothing is found or fits.
func retrieveSection(ctx context.Context, s tools.Searcher, queries []string, budget int) string {
	var b strings.Builder
	seen := map[string]bool{}
	for _, q := range queries {
		sctx, cancel := context.WithTimeout(ctx, retrieveSearchTimeout)
		hits, err := s.Search(sctx, q, retrieveHits)
		cancel()
		if err != nil {
			continue
		}
		for _, h := range hits {
			key := fmt.Sprintf("%s:%d-%d", h.File, h.StartLine, h.EndLine)
			if seen[key] {
				continue
			}
			seen[key] = true
			entry := key + "\n```\n" + strings.TrimRight(h.Text, "\n") + "\n```\n\n"
			if len(retrieveHeading)+2+b.Len()+len(entry) > budget {
				return section(b.String())
			}
			b.WriteString(entry)
		}
	}
	return section(b.String())
}

func section(entries string) string {
	if entries == "" {
		return ""
	}
	return retrieveHeading + "\n\n" + strings.TrimRight(entries, "\n")
}
