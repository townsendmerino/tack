package main

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/townsendmerino/tack/internal/loop"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/store"
)

// forkBoundary is the seq a rewind to seq forks the conversation at: the last turn.end at or before it, or 1 (the bare start)
// when no turn had finished. A rewind lands inside a turn, at a call's checkpoint, and a fork can't end inside one, so the turn
// the call was in is left out of the fork, along with everything after.
func forkBoundary(events []proto.Event, seq int64) int64 {
	boundary := int64(1)
	for _, ev := range events {
		if ev.Seq > seq {
			break
		}
		if ev.Type == proto.EvTurnEnd {
			boundary = ev.Seq
		}
	}
	return boundary
}

// forkAfterRewind makes the child session a rewind goes on in (DESIGN §6.8): the conversation up to the boundary, with the
// files already restored to their state at seq. It says what it made. A fork that fails leaves the files restored, and says so.
func forkAfterRewind(st *store.Store, session proto.SessionID, events []proto.Event, seq int64, stdout, stderr io.Writer) int {
	at := forkBoundary(events, seq)
	child := store.NewSessionID(time.Now())
	if err := loop.Fork(st, session, child, at); errors.Is(err, store.ErrForkNotDurable) {
		// The fork is complete and findable; only its directory entry is not known to be on disk.
		fmt.Fprintf(stderr, "tack rewind: warning: %v\n", err)
	} else if err != nil {
		fmt.Fprintf(stderr, "tack rewind: the files are rewound, but the conversation could not be forked at seq %d: %v\n", at, err)
		return exitError
	}
	fmt.Fprintf(stdout, "forked the conversation at seq %d as %s; what came after it is not in it\n", at, child)
	return exitOK
}
