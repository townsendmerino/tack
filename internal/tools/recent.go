package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
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
//   - a partial clone's missing object is an error, not a fetch.

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
	repo, err := git.PlainOpenWithOptions(ws.Root(), &git.PlainOpenOptions{DetectDotGit: true, EnableDotGitCommonDir: true})
	if errors.Is(err, git.ErrRepositoryNotExists) {
		return errorf("this workspace is not a git repository, so there is no history; use glob and grep instead"), nil
	}
	if err != nil {
		return errorf("could not read this repository: %v", err), nil
	}
	text, err := recentText(ctx, repo, ws.Root(), n)
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	if err != nil {
		return errorf("could not read this repository: %v", err), nil
	}
	return Result{Text: text}, nil
}

// recentText is the tool's text for repo, seen from the workspace root root.
func recentText(ctx context.Context, repo *git.Repository, root string, n int) (string, error) {
	wt, err := repo.Worktree()
	if err != nil {
		return "", err
	}
	// Paths are from the workspace root, which may be below the repository's top.
	prefix := ""
	top, _ := filepath.EvalSymlinks(wt.Filesystem.Root())
	here, _ := filepath.EvalSymlinks(root)
	if rel, err := filepath.Rel(top, here); err == nil && rel != "." {
		prefix = filepath.ToSlash(rel) + "/"
	}
	status, err := wt.Status()
	if err != nil {
		return "", err
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
