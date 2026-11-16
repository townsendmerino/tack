package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/townsendmerino/tack/internal/proto"
	snap "github.com/townsendmerino/tack/internal/snapshot"
	"github.com/townsendmerino/tack/internal/store"
)

// imagePoints marks each edit or write checkpoint in a session's points with what a rewind to it restores: the oldest
// pre-image of every path from that call on, since the oldest is the file as it was before the first of those calls touched it.
func imagePoints(points []rewindPoint) []rewindPoint {
	for i := range points {
		if len(points[i].files) == 0 {
			continue
		}
		oldest := map[string]proto.FileImage{}
		var order []string
		for _, later := range points[i:] {
			for _, f := range later.files {
				if _, ok := oldest[f.Path]; !ok {
					oldest[f.Path] = f
					order = append(order, f.Path)
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

// planImages compares the workspace with the pre-images and returns what restoring them would change, in path order. A path
// whose file already holds the pre-image is not listed.
func planImages(root string, images []proto.FileImage, blobs *store.Blobs) ([]snap.Change, error) {
	var out []snap.Change
	for _, img := range images {
		abs, err := imagePath(root, img.Path)
		if err != nil {
			return nil, err
		}
		cur, err := os.ReadFile(abs)
		missing := errors.Is(err, fs.ErrNotExist)
		if err != nil && !missing {
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
		if missing || !bytes.Equal(cur, want) {
			out = append(out, snap.Change{Path: img.Path, Kind: snap.Bring})
		}
	}
	return out, nil
}

// restoreImages writes each pre-image back, or removes the file a write created. Every blob is read before the first file is
// touched, so a missing blob changes nothing. It returns how many paths it changed.
func restoreImages(root string, images []proto.FileImage, blobs *store.Blobs) (int, error) {
	changes, err := planImages(root, images, blobs)
	if err != nil {
		return 0, err
	}
	todo := map[string]bool{}
	for _, c := range changes {
		todo[c.Path] = true
	}
	n := 0
	for _, img := range images {
		if !todo[img.Path] {
			continue
		}
		abs, _ := imagePath(root, img.Path)
		if img.Absent {
			if err := os.Remove(abs); err != nil {
				return n, err
			}
		} else {
			want, err := blobs.Get(img.Blob)
			if err != nil {
				return n, err
			}
			mode := fs.FileMode(0o644)
			if fi, err := os.Stat(abs); err == nil {
				mode = fi.Mode().Perm()
			}
			if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
				return n, err
			}
			if err := os.WriteFile(abs, want, mode); err != nil {
				return n, err
			}
		}
		n++
	}
	return n, nil
}

// imagePath is the absolute path of a pre-image's file, refusing a path that leaves the workspace. A log is data: a path in it
// grants nothing.
func imagePath(root, rel string) (string, error) {
	if !filepath.IsLocal(filepath.FromSlash(rel)) {
		return "", fmt.Errorf("the log names %q, which is outside the workspace", rel)
	}
	abs := filepath.Join(root, filepath.FromSlash(rel))
	// A symlink made since the call must not carry the write out: the nearest existing parent has to resolve inside the root.
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	for dir := filepath.Dir(abs); ; dir = filepath.Dir(dir) {
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			if real != realRoot && !strings.HasPrefix(real, realRoot+string(filepath.Separator)) {
				return "", fmt.Errorf("%q resolves outside the workspace", rel)
			}
			break
		}
		if dir == root || dir == filepath.Dir(dir) {
			break
		}
	}
	return abs, nil
}
