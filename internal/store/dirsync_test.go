package store

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// recordDirSyncs makes syncDir record each directory it syncs, then restores it.
func recordDirSyncs(t *testing.T) *[]string {
	t.Helper()
	var got []string
	orig := syncDir
	syncDir = func(dir string) error {
		got = append(got, dir)
		return orig(dir)
	}
	t.Cleanup(func() { syncDir = orig })
	return &got
}

// TestDirectoryEntriesAreSynced checks every directory entry the store creates is followed by a sync of the directory
// holding it: the store's own directories, a session's directory and log, and a blob renamed into place with each new
// directory above it (ADR-004). Without these, a synced log can survive power loss while the entries it relies on do
// not.
func TestDirectoryEntriesAreSynced(t *testing.T) {
	syncs := recordDirSyncs(t)
	parent := t.TempDir()
	root := filepath.Join(parent, "state")
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{parent, root} { // state/ is new in parent; sessions/ and blobs/ are new in state/
		if !slices.Contains(*syncs, want) {
			t.Errorf("Open did not sync %s; synced %v", want, *syncs)
		}
	}

	*syncs = nil
	l, err := s.Create("s_1")
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
	sessions := filepath.Join(root, "sessions")
	if want := []string{sessions, filepath.Join(sessions, "s_1")}; !slices.Equal(*syncs, want) {
		t.Errorf("Create synced %v, want %v (the new session directory in sessions/, then events.jsonl in it)", *syncs, want)
	}

	*syncs = nil
	h, err := s.Blobs.Put([]byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	hexsum, _ := parseHandle(h)
	blobDir := filepath.Dir(s.Blobs.path(hexsum))
	blobs := filepath.Join(root, "blobs")
	want := []string{blobs, filepath.Join(blobs, "sha256"), blobDir}
	if !slices.Equal(*syncs, want) {
		t.Errorf("Put synced %v, want %v (each new directory's parent, then the blob's directory after the rename)", *syncs, want)
	}

	// A second Put of the same bytes finds the blob durable and syncs nothing.
	*syncs = nil
	if _, err := s.Blobs.Put([]byte("hello")); err != nil || len(*syncs) != 0 {
		t.Errorf("repeat Put: %v, synced %v", err, *syncs)
	}
}

// TestBlobDirSyncFollowsTheRename checks the order inside Put: the blob's directory is synced after the blob is in
// place, so the sync covers its entry.
func TestBlobDirSyncFollowsTheRename(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var blobPresent []bool
	orig := syncDir
	var target string
	syncDir = func(dir string) error {
		if target != "" && dir == filepath.Dir(target) {
			_, err := os.Stat(target)
			blobPresent = append(blobPresent, err == nil)
		}
		return orig(dir)
	}
	t.Cleanup(func() { syncDir = orig })
	data := []byte("ordered")
	sum := sha256Hex(data)
	target = s.Blobs.path(sum)
	if _, err := s.Blobs.Put(data); err != nil {
		t.Fatal(err)
	}
	if len(blobPresent) == 0 || !blobPresent[len(blobPresent)-1] {
		t.Fatalf("the blob's directory was not synced after the blob was in place: %v", blobPresent)
	}
}
