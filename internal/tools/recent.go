package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/osfs"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/cache"
	format "github.com/go-git/go-git/v5/plumbing/format/config"
	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/filesystem"
	"github.com/go-git/go-git/v5/storage/filesystem/dotgit"
	"github.com/go-git/go-git/v5/utils/merkletrie"

	"github.com/townsendmerino/tack/internal/server"
)

// recently_changed (DESIGN §6.5; docs/design/p1-notes.md, "Decisions"): the workspace's uncommitted changes and its
// recent commits with the files each touched.
//
// It reads the repository with go-git, in this process, and runs no program. A repository's config is the workspace's
// content, and the git binary runs what that config names: fsmonitor hooks, filter drivers, textconv, gpg, remote
// helpers for a lazy fetch, and the same again inside submodules. Turning each off by name kept missing ways in, so
// tack does not run git here at all. go-git reads the index, objects and refs itself; it applies no filter, runs no
// hook, verifies no signature and fetches nothing, and it reads only the repository it is opened on (no GIT_DIR). The
// costs:
//   - a file under a filter (git-lfs's, say) can show as modified;
//   - renames show as a delete and an add;
//   - a partial clone's missing object is an error, not a fetch;
//   - a sparse checkout's per-worktree settings are not applied (readIgnorable).
//
// A .git file is followed where it points, as git follows it: a linked worktree's points outside the worktree. Only a
// directory with a HEAD is read as a repository; anything else is "not a git repository".

const (
	recentDefault  = 10
	recentMax      = 30
	recentMaxLines = 200 // the status's lines are cut here, and the log's lines too, each on its own count
)

type recentTool struct{}

func (recentTool) ReadOnly() bool { return true }

func (recentTool) Spec() server.Tool {
	return server.Tool{Name: "recently_changed",
		Description: "Uncommitted changes and the last commits (default 10, max 30), each with the files it changed.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"commits":{"type":"integer"}}}`)}
}

func (recentTool) Run(ctx context.Context, ws *Workspace, input json.RawMessage) (Result, error) {
	var in struct {
		Commits *int `json:"commits"`
	}
	if len(input) > 0 {
		if err := json.Unmarshal(input, &in); err != nil {
			return errorf("bad input: %v", err), nil
		}
	}
	n := recentDefault
	if in.Commits != nil {
		n = *in.Commits
	}
	if n < 1 || n > recentMax {
		return errorf("commits must be 1 to %d", recentMax), nil
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	repo, common, err := openRepo(ws.Root())
	if errors.Is(err, git.ErrRepositoryNotExists) {
		return errorf("this workspace is not a git repository, so there is no history; use glob and grep instead"), nil
	}
	if err != nil {
		return errorf("could not read this repository: %v", err), nil
	}
	text, err := recentText(ctx, repo, common, ws.Root(), n)
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	if err != nil {
		return errorf("could not read this repository: %v", err), nil
	}
	return Result{Text: text}, nil
}

// recentText is the tool's text for repo, whose common git directory is common, seen from the workspace root root.
func recentText(ctx context.Context, repo *git.Repository, common, root string, n int) (string, error) {
	wt, err := repo.Worktree()
	if err != nil {
		return "", err
	}
	wt.Excludes = append(wt.Excludes, excludes(repo, common)...)
	// Paths are from the workspace root, which may be below the repository's top.
	prefix := ""
	top, _ := filepath.EvalSymlinks(wt.Filesystem.Root())
	here, _ := filepath.EvalSymlinks(root)
	if rel, err := filepath.Rel(top, here); err == nil && rel != "." {
		prefix = filepath.ToSlash(rel) + "/"
	}
	// go-git's Status takes no context and walks the whole worktree: it runs on its own, and a cancelled call returns
	// at once, leaving it to finish and its answer to be dropped.
	type answer struct {
		status git.Status
		err    error
	}
	done := make(chan answer, 1)
	go func() {
		st, err := wt.Status()
		done <- answer{st, err}
	}()
	var status git.Status
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case a := <-done:
		if a.err != nil {
			return "", a.err
		}
		status = a.status
	}
	var paths []string
	for p, st := range status {
		if strings.HasPrefix(p, prefix) && (st.Staging != git.Unmodified || st.Worktree != git.Unmodified) {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	var b strings.Builder
	b.WriteString("Uncommitted changes (git status):\n")
	if len(paths) == 0 {
		b.WriteString("(none)\n")
	}
	for i, p := range paths {
		if i == recentMaxLines {
			b.WriteString("[… more; narrow with git status in bash if you have it]\n")
			break
		}
		st := status[p]
		fmt.Fprintf(&b, "%c%c %s\n", code(st.Staging), code(st.Worktree), strings.TrimPrefix(p, prefix))
	}
	fmt.Fprintf(&b, "\nLast %d commits (git log), newest first:\n", n)
	lines, shown := 0, 0
	head, err := repo.Head()
	if err != nil && !errors.Is(err, plumbing.ErrReferenceNotFound) {
		return "", err
	}
	if err == nil { // a repository with no commits has no HEAD yet
		commits, err := repo.Log(&git.LogOptions{From: head.Hash(), Order: git.LogOrderCommitterTime})
		if err != nil {
			return "", err
		}
		defer commits.Close()
		for shown < n && lines < recentMaxLines {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			c, err := commits.Next()
			if err != nil {
				break // the end of history
			}
			files, err := changed(c, prefix)
			if err != nil {
				return "", err
			}
			if len(files) == 0 && (prefix != "" || c.NumParents() <= 1) {
				continue // a commit that changes nothing here, as git log -- . leaves it out
			}
			subject, _, _ := strings.Cut(strings.TrimSpace(c.Message), "\n")
			fmt.Fprintf(&b, "%s %s %s\n", c.Hash.String()[:7], c.Author.When.Format("2006-01-02"), subject)
			shown, lines = shown+1, lines+1
			for _, f := range files {
				if lines == recentMaxLines {
					break
				}
				b.WriteString("  " + f + "\n")
				lines++
			}
		}
		if lines >= recentMaxLines {
			b.WriteString("[… more; ask for fewer commits]\n")
		}
	}
	if shown == 0 {
		b.WriteString("(none)\n")
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// code is a status's letter as git status --porcelain shows it.
func code(c git.StatusCode) byte {
	if c == git.Unmodified {
		return ' '
	}
	return byte(c)
}

// changed is the files commit c changed under prefix, against its first parent (none for a root commit), as
// "A\tpath", "D\tpath" or "M\tpath" from prefix; a merge shows none, as git log does without -m. Trees are compared by
// their entries' hashes: no blob is read.
func changed(c *object.Commit, prefix string) ([]string, error) {
	if c.NumParents() > 1 {
		return nil, nil
	}
	tree, err := c.Tree()
	if err != nil {
		return nil, err
	}
	var parent *object.Tree
	if c.NumParents() == 1 {
		p, err := c.Parent(0)
		if err != nil {
			return nil, err
		}
		if parent, err = p.Tree(); err != nil {
			return nil, err
		}
	}
	changes, err := object.DiffTree(parent, tree)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, ch := range changes {
		action, err := ch.Action()
		if err != nil {
			return nil, err
		}
		name, letter := ch.To.Name, "M"
		switch action {
		case merkletrie.Insert:
			letter = "A"
		case merkletrie.Delete:
			name, letter = ch.From.Name, "D"
		}
		if strings.HasPrefix(name, prefix) {
			out = append(out, letter+"\t"+strings.TrimPrefix(name, prefix))
		}
	}
	sort.Strings(out)
	return out, nil
}

// readIgnorable are the repository extensions a read through go-git can ignore, lowercased as go-git reads them:
// a partial clone's (a missing object is then an error, not a fetch), sparse checkout's per-worktree config (its
// settings are not applied: status reads the whole worktree), and two that change nothing for a reader. go-git refuses
// to open a repository with any extension it does not implement; any other extension (objectformat, refstorage) does
// change how the repository is read, so it is still refused, and the error names it.
var readIgnorable = map[string]bool{"partialclone": true, "worktreeconfig": true, "preciousobjects": true, "noop": true}

// readStorer is a repository's storage whose config leaves out the extensions a read can ignore (readIgnorable).
type readStorer struct{ *filesystem.Storage }

func (s readStorer) Config() (*config.Config, error) {
	cfg, err := s.Storage.Config()
	if err != nil || cfg.Raw == nil || !cfg.Raw.HasSection("extensions") {
		return cfg, err
	}
	sec := cfg.Raw.Section("extensions")
	var keep format.Options
	for _, o := range sec.Options {
		if !readIgnorable[strings.ToLower(o.Key)] {
			keep = append(keep, o)
		}
	}
	sec.Options = keep
	return cfg, nil
}

// openRepo opens the repository holding root, as git.PlainOpenWithOptions does with DetectDotGit and
// EnableDotGitCommonDir: the nearest .git at or above root, a directory or a "gitdir:" file, with its commondir. It
// opens it through readStorer, which go-git's own open has no option for.
func openRepo(root string) (repo *git.Repository, common string, err error) {
	path, err := filepath.Abs(root)
	if err != nil {
		return nil, "", err
	}
	var fi os.FileInfo
	for {
		if fi, err = os.Stat(filepath.Join(path, git.GitDirName)); err == nil {
			break
		}
		if !os.IsNotExist(err) {
			return nil, "", err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return nil, "", git.ErrRepositoryNotExists
		}
		path = parent
	}
	gitDir := filepath.Join(path, git.GitDirName)
	if !fi.IsDir() {
		b, err := os.ReadFile(gitDir)
		if err != nil {
			return nil, "", err
		}
		line, ok := strings.CutPrefix(string(b), "gitdir: ")
		if !ok {
			return nil, "", fmt.Errorf(".git file has no gitdir: line")
		}
		line, _, _ = strings.Cut(line, "\n")
		if gitDir = strings.TrimSpace(line); !filepath.IsAbs(gitDir) {
			gitDir = filepath.Join(path, gitDir)
		}
	}
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		return nil, "", git.ErrRepositoryNotExists
	}
	dot := osfs.New(gitDir)
	var fs billy.Filesystem = dot
	common = gitDir
	if b, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		if c := strings.TrimSpace(string(b)); c != "" {
			if common = c; !filepath.IsAbs(common) {
				common = filepath.Join(gitDir, common)
			}
			fs = dotgit.NewRepositoryFilesystem(dot, osfs.New(common))
		}
	}
	repo, err = git.Open(readStorer{filesystem.NewStorage(fs, cache.NewObjectLRUDefault())}, osfs.New(path))
	return repo, common, err
}

// excludes is what git status leaves out beyond the worktree's .gitignore files, which go-git reads itself: the
// repository's info/exclude (in its common directory, so a linked worktree's is its repository's), and the user's
// excludes file: core.excludesfile from the repository's config, else from ~/.gitconfig, else git's default,
// ~/.config/git/ignore. These are files of patterns, read and never run.
func excludes(repo *git.Repository, common string) []gitignore.Pattern {
	ps := patternsIn(filepath.Join(common, "info", "exclude"))
	file := ""
	if cfg, err := repo.Config(); err == nil && cfg.Raw != nil {
		file = cfg.Raw.Section("core").Option("excludesfile")
	}
	if file == "" {
		if g, err := gitignore.LoadGlobalPatterns(osfs.New("/")); err == nil && len(g) > 0 {
			return append(ps, g...)
		}
		if home, err := os.UserHomeDir(); err == nil {
			file = filepath.Join(home, ".config", "git", "ignore")
		}
	}
	if rest, ok := strings.CutPrefix(file, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			file = filepath.Join(home, rest)
		}
	}
	if file != "" {
		ps = append(ps, patternsIn(file)...)
	}
	return ps
}

// patternsIn is the gitignore patterns in file, none when it cannot be read.
func patternsIn(file string) []gitignore.Pattern {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	var ps []gitignore.Pattern
	for l := range strings.SplitSeq(string(b), "\n") {
		if l = strings.TrimRight(l, "\r"); strings.TrimSpace(l) != "" && !strings.HasPrefix(l, "#") {
			ps = append(ps, gitignore.ParsePattern(l, nil))
		}
	}
	return ps
}
