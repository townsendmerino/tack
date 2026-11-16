package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/townsendmerino/tack/internal/proto"
	snap "github.com/townsendmerino/tack/internal/snapshot"
	"github.com/townsendmerino/tack/internal/store"
)

// maxImageBytes is the most of a file the preview reads to compare it with its pre-image; a larger file is reported as changed
// without being read.
const maxImageBytes = 32 << 20

// imagePoints marks each edit or write checkpoint in a session's points with what a rewind to it restores: the oldest
// pre-image of every path from that call on, since the oldest is the file as it was before the first of those calls touched it.
// Only checkpoints of the same workspace count, and a path is keyed in its cleaned form.
func imagePoints(points []rewindPoint) []rewindPoint {
	for i := range points {
		if len(points[i].files) == 0 {
			continue
		}
		oldest := map[string]proto.FileImage{}
		var order []string
		for _, later := range points[i:] {
			if later.workspace != points[i].workspace {
				continue
			}
			for _, f := range later.files {
				key := path.Clean(f.Path)
				if _, ok := oldest[key]; !ok {
					oldest[key] = f
					order = append(order, key)
				}
			}
		}
		sort.Strings(order)
		points[i].images = nil
		for _, p := range order {
			points[i].images = append(points[i].images, oldest[p])
		}
	}
	return points
}

// imageTree is a workspace opened as an os.Root, through which every read and write of a rewind goes: a symlink, in any
// directory of a path or at the file itself, cannot carry an operation out of the workspace, nor can a swap between a check and a
// write. state is tack's state directory, which a rewind never touches even when it lies under the workspace.
type imageTree struct {
	root      *os.Root
	ws, state string
}

func openImageTree(ws, state string) (*imageTree, error) {
	r, err := os.OpenRoot(ws)
	if err != nil {
		return nil, err
	}
	return &imageTree{root: r, ws: ws, state: state}, nil
}

func (t *imageTree) close() { t.root.Close() }

// check refuses a log's path unless it is already in clean form and names a place inside the workspace and outside tack's
// state directory. A log is data: a path in it grants nothing.
func (t *imageTree) check(rel string) error {
	if !filepath.IsLocal(filepath.FromSlash(rel)) || path.Clean(rel) != rel || strings.Contains(rel, `\`) {
		return fmt.Errorf("the log names %q, which is not a clean path inside the workspace", rel)
	}
	if t.state != "" {
		if within(resolveLoose(t.state), resolveLoose(filepath.Join(t.ws, filepath.FromSlash(rel)))) {
			return fmt.Errorf("%q is inside tack's state directory", rel)
		}
	}
	return nil
}

// within reports whether p is dir or below it.
func within(dir, p string) bool {
	return p == dir || strings.HasPrefix(p, dir+string(filepath.Separator))
}

// resolveLoose is p with its symlinks resolved as far as it exists, and the rest of it appended unchanged.
func resolveLoose(p string) string {
	p = filepath.Clean(p)
	rest := ""
	for {
		if real, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(real, rest)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return filepath.Join(p, rest)
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = parent
	}
}

// current is the state of the file at rel: its content, or missing. A path that is not a regular file (a link, a directory, a
// FIFO) is an error and is never opened, so a preview cannot hang on a device. A file over maxImageBytes comes back with big
// set and no content.
func (t *imageTree) current(rel string) (data []byte, missing, big bool, err error) {
	fi, err := t.root.Lstat(filepath.FromSlash(rel))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, true, false, nil
	}
	if err != nil {
		return nil, false, false, err
	}
	if !fi.Mode().IsRegular() {
		return nil, false, false, fmt.Errorf("%q is not a regular file now, which a rewind does not follow or replace", rel)
	}
	if fi.Size() > maxImageBytes {
		return nil, false, true, nil
	}
	data, err = t.root.ReadFile(filepath.FromSlash(rel))
	return data, false, false, err
}

// plan compares the workspace with the pre-images and returns what restoring them would change, in path order. A path whose
// file already holds the pre-image is not listed.
func (t *imageTree) plan(images []proto.FileImage, blobs *store.Blobs) ([]snap.Change, error) {
	var out []snap.Change
	for _, img := range images {
		if err := t.check(img.Path); err != nil {
			return nil, err
		}
		cur, missing, big, err := t.current(img.Path)
		if err != nil {
			return nil, err
		}
		if img.Absent {
			if !missing {
				out = append(out, snap.Change{Path: img.Path, Kind: snap.Remove})
			}
			continue
		}
		want, err := blobs.Get(img.Blob)
		if err != nil {
			return nil, fmt.Errorf("the pre-image of %q: %w", img.Path, err)
		}
		if missing || big || !bytes.Equal(cur, want) {
			out = append(out, snap.Change{Path: img.Path, Kind: snap.Bring})
		}
	}
	return out, nil
}

// restore writes each pre-image back, or removes the file a write created. Every path is checked and every blob read before the
// first file is touched, so a refused path or a missing blob changes nothing. A file is written to a temporary name beside it
// and renamed over it, so a hard link to a file elsewhere is replaced and never written through. A recreated file gets mode
// 0644; a file that is there keeps its mode. It returns how many paths it changed.
func (t *imageTree) restore(images []proto.FileImage, blobs *store.Blobs) (int, error) {
	changes, err := t.plan(images, blobs)
	if err != nil {
		return 0, err
	}
	todo := map[string]bool{}
	for _, c := range changes {
		todo[c.Path] = true
	}
	data := map[string][]byte{}
	for _, img := range images {
		if todo[img.Path] && !img.Absent {
			if data[img.Path], err = blobs.Get(img.Blob); err != nil {
				return 0, err
			}
		}
	}
	n := 0
	for _, img := range images {
		if !todo[img.Path] {
			continue
		}
		rel := filepath.FromSlash(img.Path)
		if img.Absent {
			if err := t.root.Remove(rel); err != nil {
				return n, err
			}
			n++
			continue
		}
		mode := fs.FileMode(0o644)
		if fi, err := t.root.Lstat(rel); err == nil {
			mode = fi.Mode().Perm()
		}
		if err := t.root.MkdirAll(filepath.Dir(rel), 0o755); err != nil {
			return n, err
		}
		if err := t.replace(rel, data[img.Path], mode); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// replace writes data to a new file beside rel and renames it over rel.
func (t *imageTree) replace(rel string, data []byte, mode fs.FileMode) error {
	dir := filepath.Dir(rel)
	for i := 0; ; i++ {
		tmp := filepath.Join(dir, fmt.Sprintf(".tack-rewind-%d", i))
		f, err := t.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if errors.Is(err, fs.ErrExist) && i < 100 {
			continue
		}
		if err != nil {
			return err
		}
		_, werr := f.Write(data)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr == nil {
			werr = t.root.Chmod(tmp, mode) // the umask must not narrow what was there
		}
		if werr == nil {
			werr = t.root.Rename(tmp, rel)
		}
		if werr != nil {
			t.root.Remove(tmp)
		}
		return werr
	}
}
