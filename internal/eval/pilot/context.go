package pilot

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/townsendmerino/aikit/chunk"
	"github.com/townsendmerino/aikit/chunk/treesitter"
	"github.com/townsendmerino/ken/engine"
	"github.com/townsendmerino/tack/internal/kensearch"
	"github.com/townsendmerino/tack/internal/loop"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/tools"
	"mvdan.cc/sh/v3/syntax"
)

// LineSpan is a 1-based inclusive line interval [Start, End] in File, a slash path relative to the repo root.
type LineSpan struct {
	File  string `json:"file"`
	Start int    `json:"start_line"`
	End   int    `json:"end_line"`
}

// TaskGold holds a task's gold spans at its base commit: RawSpans from the non-test diff hunks, and Spans after
// widening each hunk's boundary lines to their innermost enclosing symbol in ken's tree-sitter index.
type TaskGold struct {
	Task     int        `json:"task"`
	Repo     string     `json:"repo"`
	Base     string     `json:"base"`
	Commit   string     `json:"commit,omitempty"`
	RawSpans []LineSpan `json:"raw_spans,omitempty"`
	Spans    []LineSpan `json:"spans"`
}

// ContextMetrics is one run's trajectory context score against its task's gold spans
// (docs/measurements/e1-cb3-context-metrics-preregistration-2026-10-28.md §4).
type ContextMetrics struct {
	Requests         int     `json:"requests"`
	GoldLines        int     `json:"gold_lines"`
	GoldFiles        int     `json:"gold_files"`
	SeenLines        int     `json:"seen_lines"`
	UsedLines        int     `json:"used_lines"`
	ToolOutputTokens int     `json:"tool_output_tokens"`
	FinalCov         float64 `json:"final_cov"`
	FileCov          float64 `json:"file_cov"`
	CovReq1          float64 `json:"cov_req1"`
	Cov4k            float64 `json:"cov_4k"`
	Precision        float64 `json:"precision"`
	FirstGoldReq     int     `json:"first_gold_req"`
	AUCCov           float64 `json:"auc_cov"`
	Utilization      float64 `json:"utilization"`
	EditLoc          float64 `json:"edit_loc"`
}

const (
	contextReadCapLines = 200 // tools.readMaxLines
	cov4kBudgetTokens   = 4096
)

var registerChunkerOnce sync.Once

func ensureTreesitter() {
	registerChunkerOnce.Do(func() {
		if _, err := chunk.Get(kensearch.Chunker); err != nil {
			chunk.Register(kensearch.Chunker, treesitter.New(treesitter.WithParseTimeoutMicros(1_000_000)))
		}
	})
}

// MergeSpans sorts spans by file and start line and merges overlapping or adjacent intervals.
func MergeSpans(spans []LineSpan) []LineSpan {
	var valid []LineSpan
	for _, s := range spans {
		f := cleanPath(s.File)
		if f == "" || f == "." || s.Start < 1 || s.End < s.Start {
			continue
		}
		valid = append(valid, LineSpan{File: f, Start: s.Start, End: s.End})
	}
	sort.Slice(valid, func(i, j int) bool {
		if valid[i].File != valid[j].File {
			return valid[i].File < valid[j].File
		}
		if valid[i].Start != valid[j].Start {
			return valid[i].Start < valid[j].Start
		}
		return valid[i].End < valid[j].End
	})
	var out []LineSpan
	for _, s := range valid {
		if n := len(out); n > 0 && out[n-1].File == s.File && s.Start <= out[n-1].End+1 {
			if s.End > out[n-1].End {
				out[n-1].End = s.End
			}
			continue
		}
		out = append(out, s)
	}
	return out
}

var diffHunkRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? `)

// FixGold computes a task's raw non-test diff hunks at base and widens each hunk's boundary lines to their innermost
// enclosing symbol in ken's tree-sitter index at base.
func FixGold(ctx context.Context, mirrors, repo, base, commit string, testFiles []string) (raw, widened []LineSpan, err error) {
	ensureTreesitter()
	baseMap, err := BaseTree(mirrors, repo, base)
	if err != nil {
		return nil, nil, err
	}
	gitDir := filepath.Join(mirrors, repo+".git")
	out, err := exec.CommandContext(ctx, "git", "--git-dir", gitDir, "diff", "-U0", "--no-renames", base, commit).Output()
	if err != nil {
		return nil, nil, fmt.Errorf("git diff %s %s..%s: %w", repo, base, commit, err)
	}
	testSet := map[string]bool{}
	for _, tf := range testFiles {
		testSet[cleanPath(tf)] = true
	}
	changedFiles := map[string]string{}
	file := ""
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "--- a/"):
			file = cleanPath(strings.TrimPrefix(line, "--- a/"))
		case strings.HasPrefix(line, "+++ b/"):
			bFile := strings.TrimPrefix(line, "+++ b/")
			if bFile != "/dev/null" {
				file = cleanPath(bFile)
			}
		case strings.HasPrefix(line, "@@"):
			if file == "" || !strings.HasSuffix(file, ".go") || strings.HasSuffix(file, "_test.go") || strings.Contains("/"+file, "/testdata/") || testSet[file] {
				continue
			}
			content, ok := baseMap[file]
			if !ok {
				continue
			}
			total := countTextLines(content)
			if total == 0 {
				continue
			}
			m := diffHunkRe.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			s, n := 0, 1
			_, _ = fmt.Sscan(m[1], &s)
			if m[2] != "" {
				_, _ = fmt.Sscan(m[2], &n)
			}
			var rStart, rEnd int
			if n > 0 {
				rStart = clampLine(s, 1, total)
				rEnd = clampLine(s+n-1, rStart, total)
			} else {
				rStart = clampLine(max(s, 1), 1, total)
				rEnd = clampLine(min(s+1, total), rStart, total)
			}
			raw = append(raw, LineSpan{File: file, Start: rStart, End: rEnd})
			changedFiles[file] = content
		}
	}
	if len(raw) == 0 {
		return nil, nil, nil
	}
	tmp, err := os.MkdirTemp("", "tack-fixgold-*")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(tmp)
	for rel, content := range changedFiles {
		abs := filepath.Join(tmp, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return nil, nil, err
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			return nil, nil, err
		}
	}
	idx, err := kensearch.Open(ctx, tmp, kensearch.Options{Mode: "bm25"})
	if err != nil {
		return nil, nil, err
	}
	defer idx.Close()
	for _, h := range raw {
		wStart, wEnd := h.Start, h.End
		for _, probe := range []int{h.Start, h.End} {
			defs, err := idx.SymbolAt(ctx, h.File, probe)
			if err != nil && !errors.Is(err, tools.ErrNotIndexed) {
				return nil, nil, fmt.Errorf("SymbolAt %s:%d: %w", h.File, probe, err)
			}
			if len(defs) > 0 {
				wStart = min(wStart, defs[0].StartLine)
				wEnd = max(wEnd, defs[0].EndLine)
			}
		}
		widened = append(widened, LineSpan{File: h.File, Start: wStart, End: wEnd})
	}
	return MergeSpans(raw), MergeSpans(widened), nil
}

type fileLineKey struct {
	file string
	line int
}

// trackedFile holds a file's current text and, for each 1-based current line, the inclusive [lo, hi] range of 1-based
// lines in the base commit that it came from ([0, 0] for pure insertions or files created during the run).
type trackedFile struct {
	content   string
	baseLines [][2]int
	baseTotal int
}

func newTrackedTree(base map[string]string) map[string]*trackedFile {
	out := make(map[string]*trackedFile, len(base))
	for f, content := range base {
		n := countTextLines(content)
		bl := make([][2]int, n)
		for i := range n {
			bl[i] = [2]int{i + 1, i + 1}
		}
		out[cleanPath(f)] = &trackedFile{content: content, baseLines: bl, baseTotal: n}
	}
	return out
}

// mapRange returns the base-commit LineSpans corresponding to current 1-based inclusive lines [start, end].
func (tf *trackedFile) mapRange(file string, start, end int) []LineSpan {
	if tf == nil || tf.baseTotal == 0 || len(tf.baseLines) == 0 {
		return nil
	}
	start = max(start, 1)
	end = min(end, len(tf.baseLines))
	if end < start {
		return nil
	}
	var out []LineSpan
	for i := start - 1; i < end; i++ {
		r := tf.baseLines[i]
		if r[0] >= 1 && r[1] >= r[0] {
			out = append(out, LineSpan{File: file, Start: r[0], End: min(r[1], tf.baseTotal)})
		}
	}
	return MergeSpans(out)
}

// replaceLines replaces current 1-based inclusive lines [start, end] with newContent and updates baseLines, returning
// the base-commit spans covered by the replaced region.
func (tf *trackedFile) replaceLines(file string, start, end int, newText string, newContent string) []LineSpan {
	if tf == nil {
		return nil
	}
	replaced := tf.mapRange(file, start, end)
	var bLo, bHi int
	for _, s := range replaced {
		if bLo == 0 || s.Start < bLo {
			bLo = s.Start
		}
		if s.End > bHi {
			bHi = s.End
		}
	}
	m := countTextLines(newText)
	repl := make([][2]int, m)
	if bLo > 0 && m > 0 {
		bSpan := bHi - bLo + 1
		for j := range m {
			switch {
			case j == m-1 && m < bSpan:
				repl[j] = [2]int{bLo + j, bHi}
			case j < bSpan:
				repl[j] = [2]int{bLo + j, bLo + j}
			default:
				repl[j] = [2]int{0, 0}
			}
		}
	}
	sIdx := clampLine(start-1, 0, len(tf.baseLines))
	eIdx := clampLine(end, sIdx, len(tf.baseLines))
	next := make([][2]int, 0, len(tf.baseLines)-(eIdx-sIdx)+m)
	next = append(next, tf.baseLines[:sIdx]...)
	next = append(next, repl...)
	next = append(next, tf.baseLines[eIdx:]...)
	tf.content = newContent
	tf.baseLines = next
	return replaced
}

// maxAlignCells bounds the line-alignment table rewrite builds for the part of a file that differs between its
// common prefix and suffix; past it the whole middle counts as replaced.
const maxAlignCells = 4_000_000

// rewrite replaces the file's whole text with newContent, as a write does. Lines are aligned by content (the longest
// common subsequence of lines): an unchanged line keeps its base line, and a new line has none. It returns the
// base-commit spans of the lines the write deleted or changed.
func (tf *trackedFile) rewrite(file, newContent string) []LineSpan {
	if tf == nil {
		return nil
	}
	oldLines, newLines := textLines(tf.content), textLines(newContent)
	if len(oldLines) != len(tf.baseLines) {
		// The tracked text and its line table disagree, so nothing can be aligned: every line counts as replaced.
		replaced := tf.mapRange(file, 1, len(tf.baseLines))
		tf.content, tf.baseLines = newContent, make([][2]int, len(newLines))
		return replaced
	}
	pre := 0
	for pre < len(oldLines) && pre < len(newLines) && oldLines[pre] == newLines[pre] {
		pre++
	}
	suf := 0
	for suf < len(oldLines)-pre && suf < len(newLines)-pre && oldLines[len(oldLines)-1-suf] == newLines[len(newLines)-1-suf] {
		suf++
	}
	oldMid, newMid := oldLines[pre:len(oldLines)-suf], newLines[pre:len(newLines)-suf]
	keptOld := make([]bool, len(oldMid)) // old middle lines that survive
	newFrom := make([]int, len(newMid))  // for each new middle line, its old middle index, or -1
	for j := range newFrom {
		newFrom[j] = -1
	}
	if n, m := len(oldMid), len(newMid); n > 0 && m > 0 && (n+1)*(m+1) <= maxAlignCells {
		lcs := make([]int32, (n+1)*(m+1))
		at := func(i, j int) *int32 { return &lcs[i*(m+1)+j] }
		for i := n - 1; i >= 0; i-- {
			for j := m - 1; j >= 0; j-- {
				if oldMid[i] == newMid[j] {
					*at(i, j) = *at(i+1, j+1) + 1
				} else {
					*at(i, j) = max(*at(i+1, j), *at(i, j+1))
				}
			}
		}
		for i, j := 0, 0; i < n && j < m; {
			switch {
			case oldMid[i] == newMid[j]:
				keptOld[i], newFrom[j] = true, i
				i, j = i+1, j+1
			case *at(i+1, j) >= *at(i, j+1):
				i++
			default:
				j++
			}
		}
	}
	next := make([][2]int, 0, len(newLines))
	next = append(next, tf.baseLines[:pre]...)
	for _, i := range newFrom {
		if i < 0 {
			next = append(next, [2]int{0, 0})
		} else {
			next = append(next, tf.baseLines[pre+i])
		}
	}
	next = append(next, tf.baseLines[len(oldLines)-suf:]...)
	var replaced []LineSpan
	for i, kept := range keptOld {
		if !kept {
			replaced = append(replaced, tf.mapRange(file, pre+i+1, pre+i+1)...)
		}
	}
	tf.content = newContent
	tf.baseLines = next
	return MergeSpans(replaced)
}

// textLines splits s into lines as countTextLines counts them.
func textLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

type stepSpans struct {
	seen []LineSpan
	used []LineSpan
}

// ScoreContext computes a captured tack run's ContextMetrics from its events.jsonl against base and gold.
func ScoreContext(eventsPath string, base map[string]string, gold []LineSpan, includeNotes bool) (ContextMetrics, error) {
	f, err := os.Open(eventsPath)
	if err != nil {
		return ContextMetrics{}, err
	}
	defer f.Close()

	tree := newTrackedTree(base)
	calls := map[string]proto.ToolCall{}
	var steps []stepSpans
	var spans4k []LineSpan
	cumTokens := 0

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		var ev proto.Event
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		v, err := ev.Decode()
		if err != nil {
			continue
		}
		switch p := v.(type) {
		case proto.RequestSent:
			steps = append(steps, stepSpans{})
		case proto.ToolCall:
			calls[p.ID] = p
		case proto.ToolFinished:
			c, ok := calls[p.ID]
			if !ok {
				continue
			}
			if len(steps) == 0 {
				steps = append(steps, stepSpans{})
			}
			cleanRes := loop.StripNotes(p.Result)
			seen, used := extractTackToolSpans(c.Name, c.Input, p.Outcome, p.Result, cleanRes, tree, includeNotes)
			if p.Outcome == proto.OutcomeOK || len(seen) > 0 {
				cumTokens += engine.ApproxTokens(cleanRes)
			}
			cur := &steps[len(steps)-1]
			cur.seen = append(cur.seen, seen...)
			cur.used = append(cur.used, used...)
			if cumTokens <= cov4kBudgetTokens {
				spans4k = append(spans4k, seen...)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return ContextMetrics{}, err
	}
	return computeMetrics(gold, steps, spans4k, cumTokens), nil
}

var (
	searchHeaderRe  = regexp.MustCompile(`^([^\s:]+):(\d+)-(\d+)$`)
	symbolDefRe     = regexp.MustCompile(`^([^\s:]+):(\d+)-(\d+)\s+\S+\s+\S+$`)
	numberedLineRe  = regexp.MustCompile(`^\s*(\d+)\t`)
	indentedSiteRe  = regexp.MustCompile(`^  ([^\s:]+):(\d+)\b`)
	grepMatchRe     = regexp.MustCompile(`^([^\s:]+):(\d+):`)
	editReplacedRe  = regexp.MustCompile(`lines (\d+)-(\d+)`)
	editAtLineRe    = regexp.MustCompile(`at line (\d+)`)
	currentSymNote  = regexp.MustCompile(`\[tack: the current text of [^\s]+ in ([^\s,]+), lines (\d+)-(\d+)`)
	sedRangePrintRe = regexp.MustCompile(`^(\d+)(?:,(\d+|\$))?p$`)
)

func extractTackToolSpans(name string, rawInput json.RawMessage, outcome string, fullResult, cleanResult string, tree map[string]*trackedFile, includeNotes bool) (seen, used []LineSpan) {
	if outcome != proto.OutcomeOK {
		if includeNotes && name == "edit" {
			if m := currentSymNote.FindStringSubmatch(fullResult); m != nil {
				pth := cleanPath(m[1])
				s, _ := strconv.Atoi(m[2])
				e, _ := strconv.Atoi(m[3])
				if tf := tree[pth]; tf != nil {
					seen = append(seen, tf.mapRange(pth, s, e)...)
				}
			}
		}
		return seen, nil
	}

	switch name {
	case "read":
		var in struct {
			Path   string `json:"path"`
			Offset int    `json:"offset"`
			Limit  int    `json:"limit"`
		}
		_ = json.Unmarshal(rawInput, &in)
		pth := cleanPath(in.Path)
		tf := tree[pth]
		if tf == nil || len(tf.baseLines) == 0 {
			return nil, nil
		}
		total := len(tf.baseLines)
		start, n := 1, total
		explicit := in.Offset > 0 || in.Limit > 0
		if in.Offset > 0 {
			start = in.Offset
		}
		if in.Limit > 0 {
			n = in.Limit
		} else if !explicit && total > contextReadCapLines {
			n = contextReadCapLines
		}
		if start > total {
			return nil, nil
		}
		end := min(start-1+n, total)
		return tf.mapRange(pth, start, end), nil

	case "search":
		for _, line := range strings.Split(cleanResult, "\n") {
			if m := searchHeaderRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
				pth := cleanPath(m[1])
				if tf := tree[pth]; tf != nil {
					s, _ := strconv.Atoi(m[2])
					e, _ := strconv.Atoi(m[3])
					seen = append(seen, tf.mapRange(pth, s, e)...)
				}
			}
		}
		return seen, nil

	case "symbol":
		var in struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(rawInput, &in)
		lines := strings.Split(cleanResult, "\n")
		if len(lines) > 0 {
			if m := symbolDefRe.FindStringSubmatch(lines[0]); m != nil && in.Name != "" {
				defFile := cleanPath(m[1])
				minL, maxL := 0, 0
				for _, l := range lines[1:] {
					nm := numberedLineRe.FindStringSubmatch(l)
					if nm == nil {
						break
					}
					ln, _ := strconv.Atoi(nm[1])
					if minL == 0 || ln < minL {
						minL = ln
					}
					if ln > maxL {
						maxL = ln
					}
				}
				if tf := tree[defFile]; tf != nil && minL > 0 && maxL >= minL {
					seen = append(seen, tf.mapRange(defFile, minL, maxL)...)
				}
			}
		}
		for _, l := range lines {
			if m := indentedSiteRe.FindStringSubmatch(l); m != nil {
				pth := cleanPath(m[1])
				ln, _ := strconv.Atoi(m[2])
				if tf := tree[pth]; tf != nil && ln > 0 {
					seen = append(seen, tf.mapRange(pth, ln, ln)...)
				}
			}
		}
		return seen, nil

	case "grep":
		for _, l := range strings.Split(cleanResult, "\n") {
			if m := grepMatchRe.FindStringSubmatch(l); m != nil {
				pth := cleanPath(m[1])
				ln, _ := strconv.Atoi(m[2])
				if tf := tree[pth]; tf != nil && ln > 0 {
					seen = append(seen, tf.mapRange(pth, ln, ln)...)
				}
			}
		}
		return seen, nil

	case "edit":
		var in struct {
			Path       string `json:"path"`
			OldString  string `json:"old_string"`
			NewString  string `json:"new_string"`
			ReplaceAll bool   `json:"replace_all"`
			Symbol     string `json:"symbol"`
			StartLine  int    `json:"start_line"`
			EndLine    int    `json:"end_line"`
		}
		_ = json.Unmarshal(rawInput, &in)
		pth := cleanPath(in.Path)
		tf := tree[pth]
		if tf == nil {
			return nil, nil
		}
		spans := applyTrackedEdit(tf, pth, in.OldString, in.NewString, in.ReplaceAll, in.Symbol, in.StartLine, in.EndLine, cleanResult)
		return spans, spans

	case "write":
		var in struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		_ = json.Unmarshal(rawInput, &in)
		pth := cleanPath(in.Path)
		tf := tree[pth]
		if tf == nil {
			n := countTextLines(in.Content)
			tree[pth] = &trackedFile{content: in.Content, baseLines: make([][2]int, n), baseTotal: 0}
			return nil, nil
		}
		// A write shows the model nothing of the file it replaces: it adds used lines, never seen ones.
		return nil, tf.rewrite(pth, in.Content)

	case "bash":
		var in struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal(rawInput, &in)
		return extractBashSpans(in.Command, tree), nil
	}
	return nil, nil
}

func applyTrackedEdit(tf *trackedFile, pth, oldStr, newStr string, repAll bool, symbol string, startLine, endLine int, cleanResult string) []LineSpan {
	cur := tf.content
	if startLine > 0 && endLine >= startLine {
		a, b, ok := editByteRange(cur, startLine, endLine)
		if !ok {
			return tf.mapRange(pth, startLine, endLine)
		}
		repl := newStr
		if repl != "" && strings.HasSuffix(cur[a:b], "\n") && !strings.HasSuffix(repl, "\n") {
			repl += "\n"
		}
		return tf.replaceLines(pth, startLine, endLine, repl, cur[:a]+repl+cur[b:])
	}
	if symbol != "" {
		if m := editReplacedRe.FindStringSubmatch(cleanResult); m != nil {
			s, _ := strconv.Atoi(m[1])
			e, _ := strconv.Atoi(m[2])
			a, b, ok := editByteRange(cur, s, e)
			if !ok {
				return tf.mapRange(pth, s, e)
			}
			repl := newStr
			if strings.HasSuffix(cur[a:b], "\n") && !strings.HasSuffix(repl, "\n") {
				repl += "\n"
			}
			return tf.replaceLines(pth, s, e, repl, cur[:a]+repl+cur[b:])
		}
	}
	if oldStr != "" {
		if strings.Contains(cur, oldStr) {
			if repAll {
				var matches [][2]int
				off := 0
				for {
					i := strings.Index(cur[off:], oldStr)
					if i < 0 {
						break
					}
					matches = append(matches, [2]int{off + i, off + i + len(oldStr)})
					off += i + len(oldStr)
				}
				var all []LineSpan
				for i := len(matches) - 1; i >= 0; i-- {
					m := matches[i]
					sLine, eLine := byteSpanToLines(tf.content, m[0], m[1])
					out := tf.content[:m[0]] + newStr + tf.content[m[1]:]
					all = append(all, tf.replaceLines(pth, sLine, eLine, newStr, out)...)
				}
				return MergeSpans(all)
			}
			idx := strings.Index(cur, oldStr)
			sLine, eLine := byteSpanToLines(cur, idx, idx+len(oldStr))
			out := cur[:idx] + newStr + cur[idx+len(oldStr):]
			return tf.replaceLines(pth, sLine, eLine, newStr, out)
		}
		if strings.HasSuffix(pth, ".go") && !repAll {
			if m := tools.MatchTolerances(cur, oldStr, tools.TolerancePunct); m.Name != "" {
				sLine, eLine := byteSpanToLines(cur, m.Start, m.End)
				out := cur[:m.Start] + newStr + cur[m.End:]
				return tf.replaceLines(pth, sLine, eLine, newStr, out)
			}
		}
		if m := editAtLineRe.FindStringSubmatch(cleanResult); m != nil {
			s, _ := strconv.Atoi(m[1])
			e := s + max(countTextLines(oldStr)-1, 0)
			return tf.mapRange(pth, s, e)
		}
	}
	return nil
}

// extractBashSpans parses cmd with mvdan.cc/sh/v3/syntax and extracts file spans read by cat, nl, head, tail, or sed -n.
func extractBashSpans(cmd string, tree map[string]*trackedFile) []LineSpan {
	if strings.TrimSpace(cmd) == "" {
		return nil
	}
	prog, err := syntax.NewParser().Parse(strings.NewReader(cmd), "")
	if err != nil {
		return nil
	}
	var out []LineSpan
	syntax.Walk(prog, func(node syntax.Node) bool {
		call, ok := node.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		words := make([]string, 0, len(call.Args))
		for _, w := range call.Args {
			words = append(words, wordLiteral(w))
		}
		out = append(out, bashCallSpans(words, tree)...)
		return true
	})
	return MergeSpans(out)
}

func wordLiteral(w *syntax.Word) string {
	var b strings.Builder
	for _, p := range w.Parts {
		switch pt := p.(type) {
		case *syntax.Lit:
			b.WriteString(pt.Value)
		case *syntax.SglQuoted:
			b.WriteString(pt.Value)
		case *syntax.DblQuoted:
			for _, dp := range pt.Parts {
				if lit, ok := dp.(*syntax.Lit); ok {
					b.WriteString(lit.Value)
				}
			}
		}
	}
	return b.String()
}

func bashCallSpans(words []string, tree map[string]*trackedFile) []LineSpan {
	if len(words) < 2 {
		return nil
	}
	bin := filepath.Base(words[0])
	args := words[1:]
	var spans []LineSpan
	switch bin {
	case "cat", "nl":
		for _, a := range args {
			if strings.HasPrefix(a, "-") {
				continue
			}
			pth := cleanPath(a)
			if tf := tree[pth]; tf != nil && len(tf.baseLines) > 0 {
				spans = append(spans, tf.mapRange(pth, 1, len(tf.baseLines))...)
			}
		}
	case "head":
		n := 10
		var files []string
		for i := 0; i < len(args); i++ {
			a := args[i]
			switch {
			case a == "-n" && i+1 < len(args):
				i++
				if v, err := strconv.Atoi(args[i]); err == nil && v > 0 {
					n = v
				}
			case strings.HasPrefix(a, "-n"):
				if v, err := strconv.Atoi(strings.TrimPrefix(a, "-n")); err == nil && v > 0 {
					n = v
				}
			case strings.HasPrefix(a, "--lines="):
				if v, err := strconv.Atoi(strings.TrimPrefix(a, "--lines=")); err == nil && v > 0 {
					n = v
				}
			case len(a) > 1 && a[0] == '-' && a[1] >= '0' && a[1] <= '9':
				if v, err := strconv.Atoi(a[1:]); err == nil && v > 0 {
					n = v
				}
			case !strings.HasPrefix(a, "-"):
				files = append(files, a)
			}
		}
		for _, f := range files {
			pth := cleanPath(f)
			if tf := tree[pth]; tf != nil && len(tf.baseLines) > 0 {
				spans = append(spans, tf.mapRange(pth, 1, min(n, len(tf.baseLines)))...)
			}
		}
	case "tail":
		n := 10
		fromStart := false
		var files []string
		for i := 0; i < len(args); i++ {
			a := args[i]
			parseTailN := func(s string) {
				if strings.HasPrefix(s, "+") {
					if v, err := strconv.Atoi(s[1:]); err == nil && v > 0 {
						n, fromStart = v, true
					}
				} else if v, err := strconv.Atoi(s); err == nil && v > 0 {
					n, fromStart = v, false
				}
			}
			switch {
			case a == "-n" && i+1 < len(args):
				i++
				parseTailN(args[i])
			case strings.HasPrefix(a, "-n"):
				parseTailN(strings.TrimPrefix(a, "-n"))
			case len(a) > 1 && a[0] == '-' && a[1] >= '0' && a[1] <= '9':
				parseTailN(a[1:])
			case !strings.HasPrefix(a, "-"):
				files = append(files, a)
			}
		}
		for _, f := range files {
			pth := cleanPath(f)
			if tf := tree[pth]; tf != nil && len(tf.baseLines) > 0 {
				total := len(tf.baseLines)
				if fromStart {
					spans = append(spans, tf.mapRange(pth, n, total)...)
				} else {
					spans = append(spans, tf.mapRange(pth, max(1, total-n+1), total)...)
				}
			}
		}
	case "sed":
		quiet := false
		var exprs, files []string
		for i := 0; i < len(args); i++ {
			a := args[i]
			switch {
			case a == "-n" || a == "--quiet" || a == "--silent":
				quiet = true
			case a == "-e" && i+1 < len(args):
				i++
				exprs = append(exprs, args[i])
			case strings.HasPrefix(a, "-"):
			case len(exprs) == 0:
				exprs = append(exprs, a)
			default:
				files = append(files, a)
			}
		}
		if !quiet {
			return nil
		}
		for _, ex := range exprs {
			for _, part := range strings.Split(ex, ";") {
				m := sedRangePrintRe.FindStringSubmatch(strings.TrimSpace(part))
				if m == nil {
					continue
				}
				s, _ := strconv.Atoi(m[1])
				for _, f := range files {
					pth := cleanPath(f)
					tf := tree[pth]
					if tf == nil || len(tf.baseLines) == 0 {
						continue
					}
					e := s
					if m[2] == "$" {
						e = len(tf.baseLines)
					} else if m[2] != "" {
						e, _ = strconv.Atoi(m[2])
					}
					spans = append(spans, tf.mapRange(pth, s, e)...)
				}
			}
		}
	}
	return spans
}

func computeMetrics(gold []LineSpan, steps []stepSpans, spans4k []LineSpan, cumTokens int) ContextMetrics {
	goldSet := spansToLineSet(gold)
	goldFiles := map[string]bool{}
	for k := range goldSet {
		goldFiles[k.file] = true
	}

	seenCum := map[fileLineKey]bool{}
	usedCum := map[fileLineKey]bool{}
	seenFiles := map[string]bool{}

	firstGoldReq := 0
	covReq1 := 0.0
	aucSum := 0.0

	for rIdx, st := range steps {
		addSpansToSet(seenCum, st.seen)
		addSpansToSet(usedCum, st.used)
		cov := setCoverage(goldSet, seenCum)
		if rIdx == 0 {
			covReq1 = cov
		}
		if firstGoldReq == 0 && setIntersectionCount(goldSet, seenCum) > 0 {
			firstGoldReq = rIdx + 1
		}
		aucSum += cov
	}
	for k := range seenCum {
		if goldFiles[k.file] {
			seenFiles[k.file] = true
		}
	}

	finalCov := setCoverage(goldSet, seenCum)
	fileCov := 1.0
	if len(goldFiles) > 0 {
		fileCov = float64(len(seenFiles)) / float64(len(goldFiles))
	}
	cov4k := setCoverage(goldSet, spansToLineSet(spans4k))

	precision := 1.0
	if len(seenCum) > 0 {
		precision = float64(setIntersectionCount(goldSet, seenCum)) / float64(len(seenCum))
	}

	aucCov := 0.0
	if len(steps) > 0 {
		aucCov = aucSum / float64(len(steps))
	}

	goldSeen := setIntersectionCount(goldSet, seenCum)
	goldUsed := setIntersectionCount(goldSet, usedCum)
	// Utilization counts used gold lines among the seen ones, so it stays at most 1 when a write uses lines the run
	// never saw.
	goldSeenUsed := 0
	for k := range goldSet {
		if seenCum[k] && usedCum[k] {
			goldSeenUsed++
		}
	}
	utilization := 0.0
	if goldSeen > 0 {
		utilization = float64(goldSeenUsed) / float64(goldSeen)
	}
	editLoc := 0.0
	if len(usedCum) > 0 {
		editLoc = float64(goldUsed) / float64(len(usedCum))
	}

	return ContextMetrics{
		Requests:         len(steps),
		GoldLines:        len(goldSet),
		GoldFiles:        len(goldFiles),
		SeenLines:        len(seenCum),
		UsedLines:        len(usedCum),
		ToolOutputTokens: cumTokens,
		FinalCov:         finalCov,
		FileCov:          fileCov,
		CovReq1:          covReq1,
		Cov4k:            cov4k,
		Precision:        precision,
		FirstGoldReq:     firstGoldReq,
		AUCCov:           aucCov,
		Utilization:      utilization,
		EditLoc:          editLoc,
	}
}

func spansToLineSet(spans []LineSpan) map[fileLineKey]bool {
	s := map[fileLineKey]bool{}
	addSpansToSet(s, spans)
	return s
}

func addSpansToSet(dst map[fileLineKey]bool, spans []LineSpan) {
	for _, sp := range spans {
		f := cleanPath(sp.File)
		if f == "" || sp.Start < 1 || sp.End < sp.Start {
			continue
		}
		for line := sp.Start; line <= sp.End; line++ {
			dst[fileLineKey{file: f, line: line}] = true
		}
	}
}

func setIntersectionCount(a, b map[fileLineKey]bool) int {
	if len(a) > len(b) {
		a, b = b, a
	}
	n := 0
	for k := range a {
		if b[k] {
			n++
		}
	}
	return n
}

func setCoverage(gold, seen map[fileLineKey]bool) float64 {
	if len(gold) == 0 {
		return 1.0
	}
	return float64(setIntersectionCount(gold, seen)) / float64(len(gold))
}

func countTextLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(strings.TrimSuffix(s, "\n"), "\n") + 1
}

func clampLine(v, lo, hi int) int {
	return min(max(v, lo), hi)
}

func byteSpanToLines(content string, startByte, endByte int) (startLine, endLine int) {
	startByte = clampLine(startByte, 0, len(content))
	endByte = clampLine(endByte, startByte, len(content))
	startLine = strings.Count(content[:startByte], "\n") + 1
	endTrim := endByte
	if endTrim > startByte && content[endTrim-1] == '\n' {
		endTrim--
	}
	endLine = strings.Count(content[:endTrim], "\n") + 1
	return startLine, max(endLine, startLine)
}

func editByteRange(content string, start, end int) (int, int, bool) {
	if start < 1 || end < start {
		return 0, 0, false
	}
	a, line := 0, 1
	for line < start {
		i := strings.IndexByte(content[a:], '\n')
		if i < 0 {
			return 0, 0, false
		}
		a += i + 1
		line++
	}
	b := a
	for ; line <= end; line++ {
		if b >= len(content) {
			return 0, 0, false
		}
		i := strings.IndexByte(content[b:], '\n')
		if i < 0 {
			if line < end {
				return 0, 0, false
			}
			b = len(content)
			break
		}
		b += i + 1
	}
	return a, b, true
}
