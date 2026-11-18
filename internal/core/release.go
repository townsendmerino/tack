package core

import (
	"fmt"

	"github.com/townsendmerino/tack/internal/proto"
)

// release lifts a session's hold (the guard's tripwire, DESIGN §6.6). The dispatch has already required a privileged connection with a
// person at it. A session that is not held answers the same and records nothing; a turn running is not possible while held, but the
// check is made under the same locks as a prompt's start.
func (c *Core) release(p proto.SessionReleaseParams) (any, *proto.Error) {
	s, perr := c.session(p.Session)
	if perr != nil {
		return nil, perr
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	s.mu.Lock()
	running := s.running
	s.mu.Unlock()
	if running {
		return nil, &proto.Error{Code: proto.CodeBusy, Message: "a turn is running in " + string(s.id) + "; release between turns"}
	}
	if !s.loop.Held() {
		return proto.SessionReleaseResult{}, nil
	}
	foreign, err := s.log.Wake()
	if err != nil {
		return nil, &proto.Error{Code: proto.CodeBusy, Message: fmt.Sprintf("session %s's log is open in another tack process; try again when it is done", s.id)}
	}
	for _, ev := range foreign {
		s.emit(ev)
	}
	err = s.loop.Release("user")
	s.mu.Lock()
	if !s.running {
		_ = s.log.Idle()
	}
	s.mu.Unlock()
	if err != nil {
		return nil, &proto.Error{Code: proto.CodeInternalError, Message: err.Error()}
	}
	return proto.SessionReleaseResult{}, nil
}
