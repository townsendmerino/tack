// Package proc reads what tack needs to know about a process by pid: when it started, so a record naming a pid can
// be told from a later process that reused it, and whether it still runs. The daemon's holder record and the
// supervised goinfer child's record both rely on it.
package proc
