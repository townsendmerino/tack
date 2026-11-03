// Package snapshot keeps a workspace's working tree in a shadow git repository outside the user's .git, so a command's
// changes can be rewound (DESIGN §6.8). It runs on the daemon's side of the sandbox: the state directory the shadow lives
// in is denied to commands.
//
// A snapshot holds what a command can destroy: every file under the workspace except .git, ignored files too. It holds
// no refs, no stashes and no staged state. Sockets and FIFOs aren't kept and a restore leaves them alone.
package snapshot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// Timeout bounds one Take, Restore, Changes or Prune; a lock older than this has no living holder.
	Timeout = time.Minute
	// MaxAge is how long Prune keeps a checkpoint, the newest aside.
	MaxAge = 7 * 24 * time.Hour
)

// config shuts out everything that would run or change a byte: GIT_CONFIG_* in env keeps the owner's config out, GIT_DIR
// keeps the workspace's out, and info/attributes outranks its .gitattributes.
const config = `[core]
	repositoryformatversion = 0
	bare = false
	filemode = true
	symlinks = true
	ignorecase = false
	precomposeunicode = false
	autocrlf = false
	safecrlf = false
	hooksPath = /dev/null
	fsmonitor = false
	untrackedCache = false
	attributesFile = /dev/null
	excludesFile = /dev/null
[gc]
	auto = 0
[maintenance]
	auto = false
[commit]
	gpgsign = false
`

const attributes = "* -text -eol -ident -filter -diff -merge -working-tree-encoding -export-subst\n"

// Kind is what a restore does to a path.
type Kind int

const (
	Bring  Kind = iota // it's written back, or was changed and is put back
	Remove             // it wasn't there, and goes
)

// Change is one path a restore would touch.
type Change struct {
	Path string
	Kind Kind
}

// Shadow is one workspace's shadow repository. Its methods aren't safe to call concurrently: the loop runs one command
// at a time, and a second process finds the lock and goes without a snapshot.
type Shadow struct {
	Root     string // the workspace
	Dir      string // the shadow repository
	Git      string // the git binary; "" when none was found
	MaxBytes int64  // the most a working tree may hold and still be snapshotted
	Now      func() time.Time
	Limit    time.Duration // the time a Take has; New sets Timeout

	gaveUp string // why a Take ran out of time: the next would too, at the same cost before every command
}

// New is the shadow for the workspace at root under stateDir, which is created at the first Take.
func New(stateDir, root string, maxBytes int64) *Shadow {
	root = filepath.Clean(root)
	sum := sha256.Sum256([]byte(root))
	git, _ := exec.LookPath("git")
	return &Shadow{Root: root, Dir: filepath.Join(stateDir, "shadows", hex.EncodeToString(sum[:8])+".git"), Git: git,
		MaxBytes: maxBytes, Now: time.Now, Limit: Timeout}
}

func (s *Shadow) env() []string {
	return []string{"PATH=" + os.Getenv("PATH"), "HOME=" + s.Dir, "LC_ALL=C", "GIT_DIR=" + s.Dir, "GIT_WORK_TREE=" + s.Root,
		"GIT_INDEX_FILE=" + filepath.Join(s.Dir, "index"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null", "GIT_ATTR_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "GIT_AUTHOR_NAME=tack",
		"GIT_AUTHOR_EMAIL=tack@localhost", "GIT_COMMITTER_NAME=tack", "GIT_COMMITTER_EMAIL=tack@localhost",
		fmt.Sprintf("GIT_AUTHOR_DATE=%d +0000", s.Now().Unix()), fmt.Sprintf("GIT_COMMITTER_DATE=%d +0000", s.Now().Unix())}
}

// git runs git on the shadow from the workspace, with stdin when not nil and more environment after s.env, and returns
// its stdout. The error holds the first line git said.
func (s *Shadow) git(ctx context.Context, stdin []byte, extraEnv []string, args ...string) ([]byte, error) {
	if s.Git == "" {
		return nil, errors.New("git isn't installed")
	}
	c := exec.CommandContext(ctx, s.Git, args...)
	c.Dir, c.Env = s.Root, append(s.env(), extraEnv...)
	if stdin != nil {
		c.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	c.Stdout, c.Stderr = &stdout, &stderr
	if err := c.Run(); err != nil {
		line, _, _ := strings.Cut(strings.TrimSpace(stderr.String()), "\n")
		return nil, fmt.Errorf("git %s: %v: %s", args[0], err, line)
	}
	return stdout.Bytes(), nil
}

// init creates the shadow if it isn't there yet.
func (s *Shadow) init(ctx context.Context) error {
	if _, err := os.Stat(filepath.Join(s.Dir, "info", "attributes")); err == nil {
		return nil
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	if _, err := s.git(ctx, nil, nil, "init", "-q", "--template="); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(s.Dir, "config"), []byte(config), 0o600); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(s.Dir, "info"), 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.Dir, "info", "attributes"), []byte(attributes), 0o600)
}

// manifest is what a git tree doesn't keep, as the text of the checkpoint's commit: lines "d <path>" for an empty
// directory and "m <perm> <path>" for a path whose permissions aren't the default (0644, 0755 for an executable file or
// a directory). Paths are strconv.Quote'd, since they may hold any byte but NUL and /.
type manifest struct {
	dirs  []string
	modes map[string]fs.FileMode
}

func (m manifest) String() string {
	var b strings.Builder
	for _, d := range m.dirs {
		fmt.Fprintf(&b, "d %s\n", strconv.Quote(d))
	}
	paths := make([]string, 0, len(m.modes))
	for p := range m.modes {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		fmt.Fprintf(&b, "m %04o %s\n", m.modes[p], strconv.Quote(p))
	}
	return b.String()
}

func parseManifest(text string) (manifest, error) {
	m := manifest{modes: map[string]fs.FileMode{}}
	for _, line := range strings.Split(text, "\n") {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, " ", 3)
		var err error
		switch {
		case fields[0] == "d" && len(fields) >= 2:
			var p string
			if p, err = strconv.Unquote(strings.TrimPrefix(line, "d ")); err == nil {
				m.dirs = append(m.dirs, p)
			}
		case fields[0] == "m" && len(fields) == 3:
			var mode uint64
			var p string
			if mode, err = strconv.ParseUint(fields[1], 8, 32); err == nil {
				if p, err = strconv.Unquote(fields[2]); err == nil {
					m.modes[p] = fs.FileMode(mode)
				}
			}
		default:
			err = errors.New("bad line")
		}
		if err != nil {
			return m, fmt.Errorf("the checkpoint's manifest: %q: %w", line, err)
		}
	}
	return m, nil
}

// defaultPerm is what a path has when the manifest doesn't say.
func defaultPerm(mode fs.FileMode) fs.FileMode {
	if mode.IsDir() || mode.Perm()&0o100 != 0 {
		return 0o755
	}
	return 0o644
}

var errNested = errors.New("it holds a nested repository, whose .git a snapshot can't store")

// survey walks the working tree without following a link: it returns the manifest, or why there can be no snapshot. It
// reads each directory once, since it runs before every command.
func (s *Shadow) survey(ctx context.Context) (manifest, error) {
	m := manifest{modes: map[string]fs.FileMode{}}
	var total int64
	var walk func(dir, rel string) error
	walk = func(dir, rel string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		if len(entries) == 0 && rel != "" {
			m.dirs = append(m.dirs, rel)
		}
		for _, d := range entries {
			name := filepath.Join(rel, d.Name())
			if isDotGit(d.Name()) {
				if rel == "" {
					continue
				}
				return errNested
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			switch {
			case info.Mode().IsRegular():
				if total += info.Size(); total > s.MaxBytes {
					return fmt.Errorf("the working tree is over the %d-byte cap", s.MaxBytes)
				}
			case info.IsDir():
				if err := walk(filepath.Join(dir, d.Name()), name); err != nil {
					return err
				}
			default:
				continue // a link has no permissions of its own, and git doesn't keep the rest
			}
			if perm := info.Mode().Perm(); perm != defaultPerm(info.Mode()) {
				m.modes[name] = perm
			}
		}
		return nil
	}
	return m, walk(s.Root, "")
}

// isDotGit reports whether a name is .git in any case, which a case-insensitive filesystem takes for the real one.
func isDotGit(name string) bool { return strings.EqualFold(name, ".git") }

// isTop reports whether the workspace is the top of a git repository. Inside one, a git command reaches files a
// snapshot of the workspace doesn't hold.
func (s *Shadow) isTop() bool {
	_, err := os.Lstat(filepath.Join(s.Root, ".git"))
	return err == nil
}

// Take snapshots the working tree and returns the checkpoint's id, or "" and why there is none. A tree that hasn't
// changed since the last Take returns the same id.
func (s *Shadow) Take(ctx context.Context) (id, why string) {
	if s.gaveUp != "" {
		return "", s.gaveUp
	}
	parent := ctx
	ctx, cancel := context.WithTimeout(parent, s.Limit)
	defer cancel()
	defer func() {
		if why != "" && parent.Err() == nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			s.gaveUp = "a snapshot took longer than " + s.Limit.String()
			why = s.gaveUp
		}
	}()
	if !s.isTop() {
		return "", "the workspace is not the top of a git repository"
	}
	if s.Git == "" {
		return "", "git isn't installed"
	}
	m, err := s.survey(ctx)
	if err != nil {
		return "", err.Error()
	}
	if err := s.init(ctx); err != nil {
		return "", err.Error()
	}
	s.dropStaleLock()
	if _, err := s.git(ctx, nil, nil, "add", "-A", "-f", "--", "."); err != nil {
		return "", err.Error()
	}
	out, err := s.git(ctx, nil, nil, "write-tree")
	if err != nil {
		return "", err.Error()
	}
	tree, text := strings.TrimSpace(string(out)), m.String()
	sum := sha256.Sum256([]byte(tree + "\x00" + text))
	stamp := hex.EncodeToString(sum[:])
	lastPath := filepath.Join(s.Dir, "last")
	if last, err := os.ReadFile(lastPath); err == nil {
		if have, lastID, ok := strings.Cut(string(last), " "); ok && have == stamp {
			return lastID, ""
		}
	}
	out, err = s.git(ctx, []byte(text+"\n"), nil, "commit-tree", tree)
	if err != nil {
		return "", err.Error()
	}
	id = strings.TrimSpace(string(out))
	if _, err := s.git(ctx, nil, nil, "update-ref", "refs/tack/"+id, id); err != nil {
		return "", err.Error()
	}
	if err := os.WriteFile(lastPath, []byte(stamp+" "+id), 0o600); err != nil {
		return "", err.Error()
	}
	return id, ""
}

// dropStaleLock removes an index lock older than Timeout, which a git that was killed left behind. A younger one is
// someone's, and git refuses it.
func (s *Shadow) dropStaleLock() {
	lock := filepath.Join(s.Dir, "index.lock")
	if st, err := os.Stat(lock); err == nil && time.Since(st.ModTime()) > Timeout {
		_ = os.Remove(lock)
	}
}

// known checks id names a checkpoint that's still kept.
func (s *Shadow) known(ctx context.Context, id string) error {
	if s.Git == "" {
		return errors.New("git isn't installed")
	}
	if _, err := s.git(ctx, nil, nil, "rev-parse", "-q", "--verify", "refs/tack/"+id+"^{commit}"); err != nil {
		return fmt.Errorf("checkpoint %s is gone", id)
	}
	return nil
}

// diff is what going from the checkpoint from to the checkpoint to changes, sorted by path: a path only in to is Remove,
// any other difference Bring.
func (s *Shadow) diff(ctx context.Context, from, to string) ([]Change, error) {
	out, err := s.git(ctx, nil, nil, "diff-tree", "-r", "-z", "--no-renames", "--name-status", from, to)
	if err != nil {
		return nil, err
	}
	fields := bytes.Split(bytes.TrimSuffix(out, []byte{0}), []byte{0})
	var changes []Change
	for i := 0; i+1 < len(fields); i += 2 {
		kind := Bring
		if string(fields[i]) == "A" {
			kind = Remove
		}
		changes = append(changes, Change{string(fields[i+1]), kind})
	}
	return changes, nil
}

// Changes is what a Restore of id would change in the tree as it is now. It takes a snapshot to see that, and changes
// nothing in the tree.
func (s *Shadow) Changes(ctx context.Context, id string) ([]Change, error) {
	ctx, cancel := context.WithTimeout(ctx, s.Limit)
	defer cancel()
	if err := s.known(ctx, id); err != nil {
		return nil, err
	}
	now, why := s.Take(ctx)
	if why != "" {
		return nil, fmt.Errorf("the tree as it is now can't be read: %s", why)
	}
	changes, err := s.diff(ctx, id, now)
	if err != nil {
		return nil, err
	}
	then, err := s.manifestOf(ctx, id)
	if err != nil {
		return nil, err
	}
	current, err := s.manifestOf(ctx, now)
	if err != nil {
		return nil, err
	}
	changes = append(changes, beyondTheTree(then, current, changes)...)
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes, nil
}

// beyondTheTree is what a restore changes that git's tree doesn't show, given what it does: empty directories and permissions,
// from the manifests of the checkpoint and of the tree now.
func beyondTheTree(then, now manifest, changes []Change) []Change {
	seen := map[string]bool{}
	for _, c := range changes {
		seen[c.Path] = true
	}
	holds := func(dir string) bool {
		return slices.ContainsFunc(changes, func(c Change) bool { return strings.HasPrefix(c.Path, dir+"/") })
	}
	var out []Change
	add := func(p string, k Kind) {
		if !seen[p] && !holds(p) {
			seen[p] = true
			out = append(out, Change{p, k})
		}
	}
	for _, d := range then.dirs {
		if !slices.Contains(now.dirs, d) {
			add(d, Bring)
		}
	}
	for _, d := range now.dirs {
		if !slices.Contains(then.dirs, d) {
			add(d, Remove)
		}
	}
	for p, perm := range then.modes {
		if now.modes[p] != perm {
			add(p, Bring)
		}
	}
	for p := range now.modes {
		if _, ok := then.modes[p]; !ok {
			add(p, Bring)
		}
	}
	return out
}

// manifestOf is a checkpoint's manifest.
func (s *Shadow) manifestOf(ctx context.Context, id string) (manifest, error) {
	out, err := s.git(ctx, nil, nil, "cat-file", "commit", id)
	if err != nil {
		return manifest{}, err
	}
	_, text, _ := strings.Cut(string(out), "\n\n")
	return parseManifest(text)
}

// Restore puts the working tree back as it was at id, touching only what differs, and returns the checkpoint of the tree
// it replaced, so the restore can be undone. It refuses when that checkpoint can't be taken. It writes nothing under .git.
func (s *Shadow) Restore(ctx context.Context, id string) (undo string, err error) {
	ctx, cancel := context.WithTimeout(ctx, s.Limit)
	defer cancel()
	if err := s.known(ctx, id); err != nil {
		return "", err
	}
	undo, why := s.Take(ctx)
	if why != "" {
		return "", fmt.Errorf("the tree as it is now can't be snapshotted, so a rewind couldn't be undone: %s", why)
	}
	changes, err := s.diff(ctx, id, undo)
	if err != nil {
		return "", err
	}
	m, err := s.manifestOf(ctx, id)
	if err != nil {
		return undo, err
	}
	if err := s.unlockDirs(); err != nil {
		return undo, err
	}
	// What wasn't there goes first, so a file can't stand where a directory must come back, and nothing is written
	// through a link that wasn't there either.
	var bring bytes.Buffer
	for _, c := range changes {
		if c.Kind == Remove {
			if err := os.Remove(filepath.Join(s.Root, c.Path)); err != nil && !os.IsNotExist(err) {
				return undo, err
			}
			continue
		}
		bring.WriteString(c.Path)
		bring.WriteByte(0)
	}
	if _, err := s.sweep(s.Root); err != nil {
		return undo, err
	}
	if bring.Len() > 0 {
		index := "GIT_INDEX_FILE=" + filepath.Join(s.Dir, "restore-index")
		if _, err := s.git(ctx, nil, []string{index}, "read-tree", id); err != nil {
			return undo, err
		}
		if _, err := s.git(ctx, bring.Bytes(), []string{index}, "checkout-index", "-f", "-z", "--stdin"); err != nil {
			return undo, err
		}
	}
	for _, d := range m.dirs {
		if err := os.MkdirAll(filepath.Join(s.Root, d), 0o755); err != nil {
			return undo, err
		}
	}
	return undo, s.setModes(m)
}

// unlockDirs makes every directory one the restore can work in. setModes puts them back at the end.
func (s *Shadow) unlockDirs() error {
	return filepath.WalkDir(s.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() || p == s.Root {
			return nil
		}
		if rel, err := filepath.Rel(s.Root, p); err == nil && isDotGit(rel) {
			return fs.SkipDir
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode().Perm()&0o700 == 0o700 {
			return nil
		}
		return os.Chmod(p, info.Mode().Perm()|0o700)
	})
}

// sweep removes the empty directories under dir, deepest first, and reports whether dir is empty after it. Restore makes
// the ones the checkpoint holds again.
func (s *Shadow) sweep(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	left := len(entries)
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if !e.IsDir() || (dir == s.Root && isDotGit(e.Name())) {
			continue
		}
		isEmpty, err := s.sweep(p)
		if err != nil {
			return false, err
		}
		if isEmpty {
			if err := os.Remove(p); err != nil {
				return false, err
			}
			left--
		}
	}
	return left == 0, nil
}

// setModes gives every path the permissions the checkpoint had: what the manifest says, or else the default.
func (s *Shadow) setModes(m manifest) error {
	return filepath.WalkDir(s.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == s.Root {
			return nil
		}
		rel, err := filepath.Rel(s.Root, p)
		if err != nil {
			return err
		}
		if isDotGit(rel) {
			return fs.SkipDir
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() && !info.IsDir() {
			return nil
		}
		want, ok := m.modes[rel]
		if !ok {
			want = defaultPerm(info.Mode())
		}
		if info.Mode().Perm() == want {
			return nil
		}
		return os.Chmod(p, want)
	})
}

// Prune drops checkpoints older than MaxAge, then the oldest half at a time while the shadow is over twice MaxBytes. The
// newest checkpoint always stays.
func (s *Shadow) Prune(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, s.Limit)
	defer cancel()
	if _, err := os.Stat(filepath.Join(s.Dir, "info", "attributes")); err != nil {
		return nil // nothing taken yet
	}
	out, err := s.git(ctx, nil, nil, "for-each-ref", "--sort=-committerdate", "--format=%(refname) %(committerdate:unix)", "refs/tack/")
	if err != nil {
		return err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	// Newest by date, but two in one second tie, and Take's memory of its last says which.
	if last, err := os.ReadFile(filepath.Join(s.Dir, "last")); err == nil {
		if _, id, ok := strings.Cut(string(last), " "); ok {
			if i := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, "refs/tack/"+id+" ") }); i > 0 {
				lines = append(append([]string{lines[i]}, lines[:i]...), lines[i+1:]...)
			}
		}
	}
	var refs []string // newest first
	dropped := false
	for i, line := range lines {
		ref, when, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		unix, err := strconv.ParseInt(when, 10, 64)
		if err != nil {
			return err
		}
		if i > 0 && s.Now().Sub(time.Unix(unix, 0)) > MaxAge {
			if _, err := s.git(ctx, nil, nil, "update-ref", "-d", ref); err != nil {
				return err
			}
			dropped = true
			continue
		}
		refs = append(refs, ref)
	}
	if dropped {
		if err := s.collect(ctx); err != nil {
			return err
		}
	}
	for len(refs) > 1 && s.size() > 2*s.MaxBytes {
		keep := (len(refs) + 1) / 2
		for _, ref := range refs[keep:] {
			if _, err := s.git(ctx, nil, nil, "update-ref", "-d", ref); err != nil {
				return err
			}
		}
		refs = refs[:keep]
		if err := s.collect(ctx); err != nil {
			return err
		}
	}
	return nil
}

// collect takes what Prune dropped off the disk. Take's memory of the last tree goes too, since it may name one of them.
func (s *Shadow) collect(ctx context.Context) error {
	if err := os.Remove(filepath.Join(s.Dir, "last")); err != nil && !os.IsNotExist(err) {
		return err
	}
	_, err := s.git(ctx, nil, nil, "gc", "-q", "--prune=now")
	return err
}

// size is the bytes the shadow holds on disk.
func (s *Shadow) size() int64 {
	var total int64
	_ = filepath.WalkDir(s.Dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}
