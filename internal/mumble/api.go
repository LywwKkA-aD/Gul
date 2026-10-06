package mumble

import "github.com/LywwKkA-aD/Gul/internal/session"

// Legacy transport aliases preserve compatibility while core uses neutral types.
type RawMessage = session.RawMessage
type Callbacks = session.Callbacks
type Controller = session.Controller

var _ Controller = (*Manager)(nil)
