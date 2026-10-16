package proto

import "encoding/json"

// ContentBlock is one block of a user message. P0 has text only; images and file attachments arrive with the app.
type ContentBlock struct {
	Type string `json:"type"` // "text"
	Text string `json:"text"`
}

// Text makes a text content block.
func Text(s string) ContentBlock { return ContentBlock{Type: "text", Text: s} }

// ModelBlock is one block of what the model produced, in the server-neutral form the loop sends back to the model
// on the next request (DESIGN §6.4, "Internal model"). The log keeps these exactly as received: replaying them is what
// rebuilds the request history byte for byte, which the cached prefix depends on (DESIGN §7.2).
type ModelBlock struct {
	Type      string          `json:"type"`                // "text", "thinking" or "tool_use"
	Text      string          `json:"text,omitempty"`      // text
	Thinking  string          `json:"thinking,omitempty"`  // thinking
	Signature string          `json:"signature,omitempty"` // thinking, when the server sent one
	ID        string          `json:"id,omitempty"`        // tool_use
	Name      string          `json:"name,omitempty"`      // tool_use
	Input     json.RawMessage `json:"input,omitempty"`     // tool_use: the arguments object
}
