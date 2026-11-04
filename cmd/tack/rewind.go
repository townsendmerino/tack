package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"

	"github.com/townsendmerino/tack/internal/config"
	"github.com/townsendmerino/tack/internal/halt"
	"github.com/townsendmerino/tack/internal/proto"
	snap "github.com/townsendmerino/tack/internal/snapshot"
	"github.com/townsendmerino/tack/internal/store"
)

// rewindCmd is `tack rewind` (DESIGN §6.8). With a session alone it lists what can be rewound to. With a seq it says what a
// rewind would change, and with -yes it does it, unless the rewind couldn't be undone, which takes -no-undo too. It restores the working tree only, and records a rewind event; the
// conversation fork waits for session/fork. Paths are printed quoted, since a file name can hold anything.
func rewindCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("rewind", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "config file (default ~/.config/tack/config.toml)")
	yes := fs.Bool("yes", false, "restore the files; without it, say what would change")
	noUndo := fs.Bool("no-undo", false, "with -yes, restore the files even if the rewind can't be undone")
	if err := fs.Parse(args); err != nil || fs.NArg() < 1 || fs.NArg() > 2 {
		fmt.Fprintln(stderr, "usage: tack rewind [-yes [-no-undo]] <session> [seq]")
		return exitUsage
	}
	fail := func(err any) int {
		fmt.Fprintln(stderr, "tack rewind:", err)
		return exitError
	}
	if *cfgPath == "" {
		p, err := config.Path()
		if err != nil {
			return fail(err)
		}
		*cfgPath = p
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return fail(err)
	}
	stateDir := cfg.StateDir
	if stateDir == "" {
		if stateDir, err = config.StateDir(); err != nil {
			return fail(err)
		}
	}
	st, err := store.Open(stateDir)
	if err != nil {
		return fail(err)
	}
	session := proto.SessionID(fs.Arg(0))
	events, err := st.Events(session, 0)
	if err != nil {
		return fail(err)
	}
	points := rewindPoints(events)
	if fs.NArg() == 1 {
		if len(points) == 0 {
			fmt.Fprintln(stdout, "this session has no checkpoints")
		}
		for _, p := range points {
			fmt.Fprintln(stdout, p)
		}
		return exitOK
	}
	seq, err := strconv.ParseInt(fs.Arg(1), 10, 64)
	if err != nil {
		return fail(fmt.Sprintf("%q is not a seq", fs.Arg(1)))
	}
	var point *rewindPoint
	for i := range points {
		if points[i].seq == seq {
			point = &points[i]
		}
	}
	switch {
	case point == nil:
		return fail(fmt.Sprintf("seq %d is not a checkpoint of this session", seq))
	case point.snapshot == "":
		return fail(fmt.Sprintf("the command at seq %d can't be rewound: %s", seq, point.why))
	}
	ctx := context.Background()
	shadow := snap.New(stateDir, point.workspace, cfg.Run.SnapshotMaxBytes)
	changes, err := shadow.Changes(ctx, point.snapshot)
	if err != nil {
		return fail(err)
	}
	if len(changes) == 0 {
		fmt.Fprintln(stdout, "the working tree already matches it")
		return exitOK
	}
	for _, c := range changes {
		verb := "restore"
		if c.Kind == snap.Remove {
			verb = "remove "
		}
		fmt.Fprintf(stdout, "%s %q\n", verb, c.Path)
	}
	if !*yes {
		how := "-yes"
		if why := shadow.NoUndo(ctx); why != "" {
			fmt.Fprintf(stdout, "this rewind can't be undone: %s\n", why)
			how = "-yes -no-undo"
		}
		fmt.Fprintf(stdout, "this includes anything changed outside tack; run tack rewind %s %s %d to do it\n", how, session, seq)
		return exitOK
	}
	if h, err := halt.Check(stateDir); err != nil {
		return fail(err)
	} else if h != nil {
		return fail(fmt.Sprintf("tack is halted (%s); run tack resume", halt.Path(stateDir)))
	}
	undo, why, restoreErr := shadow.Restore(ctx, point.snapshot, *noUndo)
	if errors.Is(restoreErr, snap.ErrNoUndo) {
		return fail(fmt.Sprintf("%v; tack rewind -yes -no-undo %s %d does it anyway", restoreErr, session, seq))
	}
	if errors.Is(restoreErr, snap.ErrUntouched) {
		return fail(restoreErr)
	}
	log, err := st.OpenLog(session)
	if err != nil {
		return fail(err)
	}
	defer log.Close()
	ev, err := log.Append(proto.EvRewind, seq, proto.Rewind{To: seq, Workspace: point.workspace, Snapshot: point.snapshot, Undo: undo, NoUndo: why, Changed: rewoundPaths(changes)})
	if err != nil {
		return fail(err)
	}
	back := fmt.Sprintf("tack rewind -yes %s %d undoes it", session, ev.Seq)
	if undo == "" {
		back = "it can't be undone: " + why
	}
	if restoreErr != nil {
		fmt.Fprintf(stderr, "tack rewind: it stopped partway, so the tree is part rewound: %v; %s\n", restoreErr, back)
		return exitError
	}
	fmt.Fprintf(stdout, "rewound %d path(s) to seq %d; %s\n", rewoundPaths(changes), seq, back)
	return exitOK
}

// rewindPoint is one thing a session can be rewound to.
type rewindPoint struct {
	seq       int64
	workspace string
	snapshot  string // "" when the command can't be rewound
	why       string
	what      string
}

func (p rewindPoint) String() string {
	if p.snapshot == "" {
		return fmt.Sprintf("%6d  can't be rewound (%s): %s", p.seq, p.why, p.what)
	}
	return fmt.Sprintf("%6d  %s", p.seq, p.what)
}

// rewindPoints reads them off a session's log: each command's checkpoint, and the tree each rewind replaced.
func rewindPoints(events []proto.Event) []rewindPoint {
	commands := map[string]string{}
	var points []rewindPoint
	for _, ev := range events {
		v, err := ev.Decode()
		if err != nil {
			continue
		}
		switch e := v.(type) {
		case proto.ToolCall:
			var in struct {
				Command string `json:"command"`
			}
			if json.Unmarshal(e.Input, &in) == nil {
				commands[e.ID] = in.Command
			}
		case proto.Checkpoint:
			points = append(points, rewindPoint{seq: ev.Seq, workspace: e.Workspace, snapshot: e.Snapshot, why: e.Unavailable,
				what: fmt.Sprintf("before %q", commands[e.ID])})
		case proto.Rewind:
			points = append(points, rewindPoint{seq: ev.Seq, workspace: e.Workspace, snapshot: e.Undo, why: e.NoUndo,
				what: fmt.Sprintf("before the rewind to seq %d", e.To)})
		}
	}
	return points
}

// warnNoRewind says, once per reason in a run, why its commands can't be rewound.
func warnNoRewind(w io.Writer, seen map[string]bool, why string) {
	if why == "" || seen[why] {
		return
	}
	seen[why] = true
	fmt.Fprintf(w, "tack: this session's commands can't be rewound: %s\n", why)
}

// rewoundPaths is how many paths a rewind touches: a file for a directory is one path removed and brought back.
func rewoundPaths(changes []snap.Change) int {
	paths := map[string]bool{}
	for _, c := range changes {
		paths[c.Path] = true
	}
	return len(paths)
}
