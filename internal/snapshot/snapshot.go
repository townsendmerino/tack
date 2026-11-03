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
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
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

// Shadow is one workspace's shadow repository. Its methods aren't safe to call concurrently from one process: the loop runs one
// command at a time. Between processes, Take, Restore and Prune hold a lock on the shadow (lock), and a second one goes without.
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

// lock takes the shadow for this process, without waiting, and returns how to let it go, or why it can't. A flock goes with its
// holder, so it can't go stale.
func (s *Shadow) lock() (release func(), why string) {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return nil, err.Error()
	}
	f, err := os.OpenFile(filepath.Join(s.Dir, "tack.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err.Error()
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, "another session is using this workspace's snapshots"
	}
	return func() { f.Close() }, ""
}

// Take snapshots the working tree and returns the checkpoint's id, or "" and why there is none. A tree that hasn't
// changed since the last Take returns the same id.
func (s *Shadow) Take(ctx context.Context) (id, why string) {
	if s.gaveUp != "" {
		return "", s.gaveUp
	}
	if !s.isTop() {
		return "", "the workspace is not the top of a git repository"
	}
	release, why := s.lock()
	if why != "" {
		return "", why
	}
	defer release()
	return s.take(ctx)
}

// take is Take with the shadow already locked.
func (s *Shadow) take(ctx context.Context) (id, why string) {
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
			if why := s.markUsed(lastID); why != "" {
				return "", why
			}
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

// markUsed records now as when a command last relied on a checkpoint it got again, which is what Prune ages it from: the file's
// time in used/. It returns why it couldn't, since a checkpoint that Prune might take too soon isn't one to rely on.
func (s *Shadow) markUsed(id string) string {
	dir := filepath.Join(s.Dir, "used")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err.Error()
	}
	p := filepath.Join(dir, id)
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		return err.Error()
	}
	if err := os.Chtimes(p, s.Now(), s.Now()); err != nil {
		return err.Error()
	}
	return ""
}

// lastUsed is when a checkpoint was last relied on, or made, whichever Prune can read: unix is its commit's date.
func (s *Shadow) lastUsed(ref string, unix int64) time.Time {
	if st, err := os.Stat(filepath.Join(s.Dir, "used", strings.TrimPrefix(ref, "refs/tack/"))); err == nil {
		return st.ModTime()
	}
	return time.Unix(unix, 0)
}

// Staged reports whether the user's index differs from HEAD, so a command that overwrites it would lose content a snapshot doesn't
// hold, or whether it can't tell, which counts as yes. It reads the user's repository, so it turns off what its config could run,
// takes no optional lock, and compares only the index with the tree: nothing runs and nothing under .git is written.
func (s *Shadow) Staged(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, s.Limit)
	defer cancel()
	c := exec.CommandContext(ctx, s.Git, "-C", s.Root, "-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null",
		"diff-index", "--cached", "--quiet", "HEAD", "--")
	c.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + s.Dir, "LC_ALL=C", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null", "GIT_ATTR_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0"}
	return c.Run() != nil
}

// NoUndo says why a rewind right now couldn't be undone, or "" when, as far as a walk of the tree can tell, it could.
func (s *Shadow) NoUndo(ctx context.Context) string {
	if !s.isTop() {
		return "the workspace is not the top of a git repository"
	}
	if _, err := s.survey(ctx); err != nil {
		return err.Error()
	}
	return ""
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
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("checkpoint %s is gone", id)
	}
	return nil
}

// Changes is what a Restore of id would change in the tree as it is now, sorted by path. It compares the tree with the
// checkpoint directly, so it writes nothing and works on a tree that couldn't be snapshotted.
func (s *Shadow) Changes(ctx context.Context, id string) ([]Change, error) {
	ctx, cancel := context.WithTimeout(ctx, s.Limit)
	defer cancel()
	if err := s.known(ctx, id); err != nil {
		return nil, err
	}
	d, err := s.differences(ctx, id)
	if err != nil {
		return nil, err
	}
	return d.changes, nil
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

// difference is what going back to a checkpoint takes: changes as Changes says them, and of those, the paths to remove and the
// files to write. The rest of the changes are empty directories and modes, which Restore always puts right.
type difference struct {
	changes  []Change
	remove   []string
	checkout []string
	manifest manifest
}

// differences compares the tree with the checkpoint without taking anything from git but the checkpoint. A directory the
// checkpoint holds nothing under goes as one thing, what it holds not looked at, so a nested repository or an unreadable
// directory is no obstacle. One it does hold that can't be read is opened for the length of the look and put back as it was.
// Sockets and FIFOs are left alone.
func (s *Shadow) differences(ctx context.Context, id string) (difference, error) {
	var d difference
	var err error
	if d.manifest, err = s.manifestOf(ctx, id); err != nil {
		return d, err
	}
	out, err := s.git(ctx, nil, nil, "ls-tree", "-r", "-z", id)
	if err != nil {
		return d, err
	}
	type entry struct{ mode, oid string }
	files := map[string]entry{}
	holds := map[string]bool{} // directories with something of the checkpoint's under them, or that it holds empty
	hold := func(p string) {
		for dir := filepath.Dir(p); dir != "."; dir = filepath.Dir(dir) {
			holds[dir] = true
		}
	}
	for _, rec := range bytes.Split(bytes.TrimSuffix(out, []byte{0}), []byte{0}) {
		meta, path, ok := strings.Cut(string(rec), "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 {
			continue
		}
		files[path] = entry{fields[0], fields[2]}
		hold(path)
	}
	emptyDirs := map[string]bool{}
	for _, dir := range d.manifest.dirs {
		emptyDirs[dir], holds[dir] = true, true
		hold(dir)
	}
	bring := func(p string) { d.changes = append(d.changes, Change{p, Bring}) }
	drop := func(p string) {
		d.changes = append(d.changes, Change{p, Remove})
		d.remove = append(d.remove, p)
	}
	permWant := func(p string, mode fs.FileMode) fs.FileMode {
		if perm, ok := d.manifest.modes[p]; ok {
			return perm
		}
		return defaultPerm(mode)
	}
	seen := map[string]bool{}
	var walk func(dir, rel string) error
	walk = func(dir, rel string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil // what the checkpoint holds under it reads as missing, and is written back
		}
		for _, de := range entries {
			name := filepath.Join(rel, de.Name())
			if rel == "" && isDotGit(de.Name()) {
				continue
			}
			info, err := de.Info()
			if err != nil {
				return err
			}
			switch {
			case info.IsDir():
				if !holds[name] {
					drop(name)
					continue
				}
				seen[name] = true
				if info.Mode().Perm() != permWant(name, info.Mode()) {
					bring(name)
				}
				sub := filepath.Join(dir, de.Name())
				perm := info.Mode().Perm()
				if perm&0o500 != 0o500 {
					if err := os.Chmod(sub, perm|0o700); err == nil {
						defer os.Chmod(sub, perm)
					}
				}
				if err := walk(sub, name); err != nil {
					return err
				}
			case info.Mode().IsRegular() || info.Mode()&fs.ModeSymlink != 0:
				e, ok := files[name]
				if !ok {
					drop(name)
					continue
				}
				seen[name] = true
				switch same, err := s.sameAs(filepath.Join(dir, de.Name()), info, e.mode, e.oid); {
				case err != nil || !same:
					bring(name)
					d.checkout = append(d.checkout, name)
				case info.Mode().IsRegular() && info.Mode().Perm() != permWant(name, info.Mode()):
					bring(name)
				}
			}
		}
		return nil
	}
	if err := walk(s.Root, ""); err != nil {
		return d, err
	}
	for p := range files {
		if !seen[p] {
			bring(p)
			d.checkout = append(d.checkout, p)
		}
	}
	for p := range emptyDirs {
		if !seen[p] {
			bring(p)
		}
	}
	sort.SliceStable(d.changes, func(i, j int) bool {
		if d.changes[i].Path != d.changes[j].Path {
			return d.changes[i].Path < d.changes[j].Path
		}
		return d.changes[i].Kind < d.changes[j].Kind
	})
	return d, nil
}

// sameAs reports whether a file or link on disk is what git's tree holds for it: the kind, the executable bit, and the blob's
// hash as git computes it. An unreadable file isn't known to be the same.
func (s *Shadow) sameAs(p string, info fs.FileInfo, mode, oid string) (bool, error) {
	h := sha1.New()
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		if mode != "120000" {
			return false, nil
		}
		target, err := os.Readlink(p)
		if err != nil {
			return false, err
		}
		fmt.Fprintf(h, "blob %d\x00%s", len(target), target)
	default:
		want := "100644"
		if info.Mode().Perm()&0o100 != 0 {
			want = "100755"
		}
		if mode != want {
			return false, nil
		}
		f, err := os.Open(p)
		if os.IsPermission(err) { // opened for the length of the look, and put back as it was
			if os.Chmod(p, info.Mode().Perm()|0o400) == nil {
				defer os.Chmod(p, info.Mode().Perm())
				f, err = os.Open(p)
			}
		}
		if err != nil {
			return false, err
		}
		defer f.Close()
		fmt.Fprintf(h, "blob %d\x00", info.Size())
		if _, err := io.Copy(h, f); err != nil {
			return false, err
		}
	}
	return hex.EncodeToString(h.Sum(nil)) == oid, nil
}

// ErrUntouched marks a Restore that failed before changing anything.
var ErrUntouched = errors.New("nothing was changed")

// Restore puts the working tree back as it was at id, touching only what differs. It returns the checkpoint of the tree it
// replaced, so the restore can be undone, or "" and why there is none: a tree a rule keeps out of a snapshot can still go back.
// Running out of time is another matter, and it refuses rather than go on with no way back. A failure before anything changed
// is ErrUntouched. The look, the undo snapshot and the rewrite each have s.Limit. It writes nothing under .git.
func (s *Shadow) Restore(parent context.Context, id string) (undo, noUndo string, err error) {
	release, why := s.lock()
	if why != "" {
		return "", "", fmt.Errorf("%w: %s", ErrUntouched, why)
	}
	defer release()
	d, err := func() (difference, error) {
		ctx, cancel := context.WithTimeout(parent, s.Limit)
		defer cancel()
		if err := s.known(ctx, id); err != nil {
			return difference{}, err
		}
		return s.differences(ctx, id)
	}()
	if err != nil {
		return "", "", fmt.Errorf("%w: %w", ErrUntouched, err)
	}
	undo, noUndo = s.take(parent)
	if err := parent.Err(); err != nil {
		return "", "", fmt.Errorf("%w: %w", ErrUntouched, err)
	}
	if undo == "" && s.gaveUp != "" {
		return "", "", fmt.Errorf("%w: the snapshot that would undo this ran out of time: %s", ErrUntouched, noUndo)
	}
	ctx, cancel := context.WithTimeout(parent, s.Limit)
	defer cancel()
	m := d.manifest
	if err := s.unlockDirs(); err != nil {
		return undo, noUndo, err
	}
	// What wasn't there goes first, so a file can't stand where a directory must come back, and nothing is written
	// through a link that wasn't there either.
	for _, p := range d.remove {
		if err := ctx.Err(); err != nil {
			return undo, noUndo, err
		}
		if err := os.RemoveAll(filepath.Join(s.Root, p)); err != nil {
			return undo, noUndo, err
		}
	}
	var bring bytes.Buffer
	for _, p := range d.checkout {
		bring.WriteString(p)
		bring.WriteByte(0)
	}
	if _, err := s.sweep(s.Root); err != nil {
		return undo, noUndo, err
	}
	if bring.Len() > 0 {
		index := "GIT_INDEX_FILE=" + filepath.Join(s.Dir, "restore-index")
		if _, err := s.git(ctx, nil, []string{index}, "read-tree", id); err != nil {
			return undo, noUndo, err
		}
		if _, err := s.git(ctx, bring.Bytes(), []string{index}, "checkout-index", "-f", "-z", "--stdin"); err != nil {
			return undo, noUndo, err
		}
	}
	for _, dir := range m.dirs {
		if err := os.MkdirAll(filepath.Join(s.Root, dir), 0o755); err != nil {
			return undo, noUndo, err
		}
	}
	return undo, noUndo, s.setModes(m)
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

// Prune drops checkpoints a command last relied on over MaxAge ago, then the least recently used half at a time while the shadow
// is over twice MaxBytes. The most recently used always stays.
func (s *Shadow) Prune(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, s.Limit)
	defer cancel()
	if _, err := os.Stat(filepath.Join(s.Dir, "info", "attributes")); err != nil {
		return nil // nothing taken yet
	}
	release, why := s.lock()
	if why != "" {
		return nil // another session is in there, and the next one prunes
	}
	defer release()
	out, err := s.git(ctx, nil, nil, "for-each-ref", "--sort=-committerdate", "--format=%(refname) %(committerdate:unix)", "refs/tack/")
	if err != nil {
		return err
	}
	type checkpoint struct {
		ref  string
		used time.Time
	}
	var list []checkpoint
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		ref, when, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		unix, err := strconv.ParseInt(when, 10, 64)
		if err != nil {
			return err
		}
		list = append(list, checkpoint{ref, s.lastUsed(ref, unix)})
	}
	// Newest first is most recently used first too, since only the newest can be used again. Two in one second tie, and Take's
	// memory of its last says which.
	if last, err := os.ReadFile(filepath.Join(s.Dir, "last")); err == nil {
		if _, id, ok := strings.Cut(string(last), " "); ok {
			if i := slices.IndexFunc(list, func(c checkpoint) bool { return c.ref == "refs/tack/"+id }); i > 0 {
				list = append(append([]checkpoint{list[i]}, list[:i]...), list[i+1:]...)
			}
		}
	}
	var refs []string // most recently used first
	dropped := false
	for i, c := range list {
		if i > 0 && s.Now().Sub(c.used) > MaxAge {
			if err := s.dropRef(ctx, c.ref); err != nil {
				return err
			}
			dropped = true
			continue
		}
		refs = append(refs, c.ref)
	}
	if dropped {
		if err := s.collect(ctx); err != nil {
			return err
		}
	}
	for len(refs) > 1 && s.size() > 2*s.MaxBytes {
		keep := (len(refs) + 1) / 2
		for _, ref := range refs[keep:] {
			if err := s.dropRef(ctx, ref); err != nil {
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

// dropRef ends a checkpoint.
func (s *Shadow) dropRef(ctx context.Context, ref string) error {
	if _, err := s.git(ctx, nil, nil, "update-ref", "-d", ref); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(s.Dir, "used", strings.TrimPrefix(ref, "refs/tack/")))
	return nil
}

// collect takes what Prune dropped off the disk.
func (s *Shadow) collect(ctx context.Context) error {
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
