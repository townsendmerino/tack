package store

import (
	"os"
	"path/filepath"
)

// A file's contents being durable is not enough: after power loss, a new file or a renamed blob is only reachable if
// its directory entry is durable too. Every directory entry the store creates, a new directory, a session's log, a
// blob renamed into place, is therefore followed by a sync of the directory that holds it, before the call that made
// it returns (ADR-004).

// syncDir makes a directory's entries durable. On darwin File.Sync is F_FULLFSYNC, as for the log itself (ADR-004).
// Tests replace it to see which directories are synced.
var syncDir = func(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// mkdirAllSynced is os.MkdirAll that syncs the parent of every directory it creates, top down, so a new directory is
// reachable after power loss. Directories that already exist are left alone.
func mkdirAllSynced(path string, perm os.FileMode) error {
	var missing []string
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		_, err := os.Stat(p)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			return err
		}
		missing = append(missing, p)
		if filepath.Dir(p) == p {
			break
		}
	}
	for i := len(missing) - 1; i >= 0; i-- {
		if err := os.Mkdir(missing[i], perm); err != nil && !os.IsExist(err) {
			return err
		}
		if err := syncDir(filepath.Dir(missing[i])); err != nil {
			return err
		}
	}
	return nil
}
