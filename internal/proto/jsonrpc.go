package proto

import "encoding/json"

// Version is the protocol version this build speaks. initialize exchanges it; the daemon serves this one and the
// previous one (DESIGN §6.1).
const Version = 1

// Request is a JSON-RPC 2.0 request, or a notification when ID is empty. Messages are newline-delimited (DESIGN §9.1).
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is a JSON-RPC 2.0 response: exactly one of Result and Error is set.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Error is a JSON-RPC error object.
type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *Error) Error() string { return e.Message }

// JSON-RPC error codes: the standard ones, then tack's, in the implementation-defined server range.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603

	CodeUnknownSession = -32001 // the session id names no session
	CodeHalted         = -32002 // the daemon is halted; no new turns until daemon/resume
	CodeBusy           = -32003 // the session already has a running turn
)
