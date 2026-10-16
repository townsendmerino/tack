package loop

import "github.com/townsendmerino/tack/internal/server"

func serverValidate(s *Session) error {
	req := s.Conversation().Request()
	return server.Validate(req)
}
