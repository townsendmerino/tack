package main

import (
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/store"
)

// lastRecorded maps each path an edit or write changed to what the last such call left there: the handle of its content, or
// proto.FileAbsent (ToolFinished.After). A call whose finish recorded none (a log from before it was recorded, or a call that
// never finished) leaves its path out, so a rewind has nothing to compare and says nothing about it.
func lastRecorded(events []proto.Event) map[string]string {
	paths := map[string][]string{} // tool call id -> the files its checkpoint names
	after := map[string]string{}
	for _, ev := range events {
		v, err := ev.Decode()
		if err != nil {
			continue
		}
		switch e := v.(type) {
		case proto.Checkpoint:
			for _, f := range e.Files {
				paths[e.ID] = append(paths[e.ID], f.Path)
			}
		case proto.ToolFinished:
			if e.After == "" {
				continue
			}
			for _, p := range paths[e.ID] {
				after[p] = e.After
			}
		}
	}
	return after
}

// changedOutside is which of the paths a rewind would change no longer hold what the last recorded call left there: the file
// was edited, replaced, deleted or created by something other than tack since. A path with no record is not listed.
func (t *imageTree) changedOutside(changes []string, recorded map[string]string) []string {
	var out []string
	for _, p := range changes {
		want, ok := recorded[p]
		if !ok {
			continue
		}
		data, missing, big, err := t.current(p)
		switch {
		case err != nil || big:
			out = append(out, p) // not a file tack wrote, or too large to be the one it did
		case missing:
			if want != proto.FileAbsent {
				out = append(out, p)
			}
		case want == proto.FileAbsent || store.HandleOf(data) != want:
			out = append(out, p)
		}
	}
	return out
}
