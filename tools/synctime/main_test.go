package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewerFindsACacheWrittenBesideTheModel(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.int4.metal.giw"), nil, 0o644)
	before := giwFiles(dir)
	os.WriteFile(filepath.Join(dir, "m.gguf"), nil, 0o644)
	os.WriteFile(filepath.Join(dir, "m.int8.metal.v17.giw"), nil, 0o644)
	if got := newer(giwFiles(dir), before); len(got) != 1 || got[0] != "m.int8.metal.v17.giw" {
		t.Fatalf("newer = %v", got)
	}
}
