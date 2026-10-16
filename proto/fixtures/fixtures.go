// Package fixtures embeds the golden protocol transcripts (DESIGN §9.3). Each *.jsonl file is one transcript: one
// line per message, {"dir": "c2d" (client to daemon) or "d2c", "msg": <a JSON-RPC message>}. Go tests read them through
// FS; Swift tests read the files directly.
package fixtures

import "embed"

// FS holds every transcript.
//
//go:embed *.jsonl
var FS embed.FS
