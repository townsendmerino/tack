package core

import (
	"errors"
	"io/fs"
	"time"

	"github.com/townsendmerino/tack/internal/loop"
	"github.com/townsendmerino/tack/internal/proto"
	"github.com/townsendmerino/tack/internal/store"
)

// fork is session/fork: a new session made from the first p.AtSeq events of p.Session (store.Store.Fork). It reads the parent's
// log and never opens it for writing, so a parent that is running or open elsewhere is untouched. The child is not opened
// here: the first call that names it reopens it from its log, as it would after a restart.
func (c *Core) fork(p proto.SessionForkParams) (any, *proto.Error) {
	if !store.ValidSessionID(p.Session) {
		return nil, &proto.Error{Code: proto.CodeUnknownSession, Message: "no session " + string(p.Session)}
	}
	child := store.NewSessionID(time.Now())
	switch err := loop.Fork(c.opt.Store, p.Session, child, p.AtSeq); {
	case err == nil:
		return proto.SessionForkResult{Session: child}, nil
	case errors.Is(err, fs.ErrNotExist):
		return nil, &proto.Error{Code: proto.CodeUnknownSession, Message: "no session " + string(p.Session)}
	case errors.Is(err, store.ErrForkSeq), errors.Is(err, store.ErrForkInTurn):
		return nil, &proto.Error{Code: proto.CodeInvalidParams, Message: err.Error()}
	default:
		return nil, &proto.Error{Code: proto.CodeInternalError, Message: err.Error()}
	}
}
