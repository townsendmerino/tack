// Package config reads ~/.config/tack/config.toml (DESIGN §5.3). Every setting is a field here, validated when the
// file is loaded; an unknown key is an error, so a typo fails loudly instead of being ignored (CLAUDE.md: settings
// are config, not environment).
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Config is the whole file.
type Config struct {
	// Main names the server in Servers that the main role (the loop) uses.
	Main    string            `toml:"main"`
	Servers map[string]Server `toml:"servers"`
	Run     Run               `toml:"run"`
	// Ken turns on ken's engine, the search and symbol tools and edit's symbol form; absent means none of them (DESIGN §12).
	Ken *Ken `toml:"ken"`
	// StateDir overrides where sessions, blobs and the supervised goinfer's files live; empty means the platform's
	// default (StateDir).
	StateDir string `toml:"state_dir"`
}

// Server kinds.
const (
	KindChild  = "goinfer-child"  // a goinfer-serve tack starts and supervises on this machine (DESIGN §8.1)
	KindRemote = "goinfer-remote" // a goinfer-serve running elsewhere, e.g. on nobara (DESIGN §8.2)
)

// Server is one model server.
type Server struct {
	Kind string `toml:"kind"`
	// Model is the model id to request. For a child it is also the name the model is loaded under.
	Model string `toml:"model"`

	// goinfer-child
	Binary     string   `toml:"binary"`      // path to goinfer-serve
	ModelPath  string   `toml:"model_path"`  // the checkpoint, read from ~/models (never the archive)
	Ctx        int      `toml:"ctx"`         // explicit context: goinfer's planner admits or refuses it as asked
	Args       []string `toml:"args"`        // extra goinfer-serve flags, e.g. ["-backend", "metal"]
	IdleUnload Duration `toml:"idle_unload"` // unload the model after this long without a request (default 10m)
	IdleExit   Duration `toml:"idle_exit"`   // stop the child after this long without a request (default 30m)

	// goinfer-remote
	URL string `toml:"url"`
	// TokenFile holds the server's bearer token (mode 0600). The Keychain replaces it (DESIGN §5.3).
	TokenFile string `toml:"token_file"`

	// Sampling sent with every request, for either kind; unset leaves goinfer's default (or the model's own, where its
	// GGUF recommends one).
	Temperature *float64 `toml:"temperature"` // at least 0
	TopP        *float64 `toml:"top_p"`       // above 0, at most 1
	TopK        *int     `toml:"top_k"`       // at least 1
}

// Ken configures ken's engine (ken v1.11.0, engine.Options). ModelDir is always explicit: tack does not rely on ken's
// ~/.ken/model default (DESIGN §12).
type Ken struct {
	Mode     string `toml:"mode"`      // "bm25", "semantic", "hybrid" (default) or "hybrid-rerank"
	ModelDir string `toml:"model_dir"` // the Model2Vec directory; required for every mode but bm25
}

// Run holds a turn's budgets for `tack run` (DESIGN §6.3).
type Run struct {
	MaxRequests  int      `toml:"max_requests"`   // model requests per turn (default 40)
	MaxToolCalls int      `toml:"max_tool_calls"` // tool calls per turn (default 100)
	MaxWall      Duration `toml:"max_wall"`       // wall time per turn (default 30m)
	MaxTokens    int      `toml:"max_tokens"`     // output tokens per request (default 2048; it comes out of the window)
	// TurnTokens and SessionTokens count the tokens the server computes: prefilled plus output (loop.Budget).
	TurnTokens       int `toml:"turn_tokens"`        // per turn (default 200000)
	MaxErrors        int `toml:"max_errors"`         // tool calls in a row ending in an error or a denial (default 8)
	SessionTokens    int `toml:"session_tokens"`     // per session (default 2000000)
	SessionToolCalls int `toml:"session_tool_calls"` // per session (default 1000)
	// FitWindow caps each tool result at the room the window has left (loop.Config.FitWindow). Default off until the
	// calibration adopts it (e1-calibration-preregistration-2026-10-18.md §4.6).
	FitWindow bool `toml:"fit_window"`
	// StopRepeats ends a turn when one call repeats unchanged (loop.Config.StopRepeats). Default off until the calibration
	// adopts it (e1-calibration-preregistration-2026-10-18.md §4.5).
	StopRepeats bool `toml:"stop_repeats"`
	// MaskObservations replaces superseded reads and old command output with one-line stubs once the prompt passes 75% of
	// the window, a compaction recorded as an event (loop.Config.MaskObservations; calibration §15.2). Default off.
	MaskObservations bool `toml:"mask_observations"`
	// ResultNotes says what to do after an empty search or an unchanged repeat (loop.Config.ResultNotes). Default off until
	// the calibration adopts it (e1-calibration-preregistration-2026-10-18.md §4.4).
	ResultNotes bool `toml:"result_notes"`
	// EditMessages makes a failed edit say where the text or the file is instead (tools.Options.EditMessages). Default off until
	// the calibration adopts it (e1-calibration-preregistration-2026-10-18.md §4.1).
	EditMessages bool `toml:"edit_messages"`
	// NoInstructions loads no instruction file, even under --instructions whole (calibration §4.8: whole against none).
	NoInstructions bool `toml:"no_instructions"`
	// InstructionsHead loads only the instruction file's budgeted head: its first section and an outline of its headings,
	// capped at 15% of the window (core.Options.InstructionsHead; calibration §12.12).
	InstructionsHead bool `toml:"instructions_head"`
	// RefuseDuplicateEdits refuses an edit identical to one already applied to the same file this session
	// (tools.Options.RefuseDuplicateEdits; calibration §12.14). Default off.
	RefuseDuplicateEdits bool `toml:"refuse_duplicate_edits"`
	// VerifyDone refuses an edit task's done, up to 3 times, until a go build, vet or test has run since the last change
	// (loop.Config.VerifyDone; calibration §4.2, §12.15). Default off.
	VerifyDone bool `toml:"verify_done"`
	// BuildAfterEdit builds a .go file's package after each successful edit or write and appends its errors when it does not
	// build (loop.Config.BuildAfterEdit; a C6 candidate, not yet registered). Default off.
	BuildAfterEdit bool `toml:"build_after_edit"`
	// RestateTask makes the refusal of an edit task's done with no change quote the task back (loop.Config.RestateTask; a C6
	// candidate, not yet registered). Default off.
	RestateTask bool `toml:"restate_task"`
	// EditLines gives edit its line form, start_line and end_line in place of old_string (tools.Options.LineEdits;
	// calibration §4.9, a candidate, not yet registered). Default off.
	EditLines bool `toml:"edit_lines"`
	// CurrentSymbol gives a repeated edit whose text is not in the file a note with the current text of the symbol it aimed
	// at (core.Options.CurrentSymbol; calibration §12.20, a candidate). Without [ken], tack opens ken's index in bm25 mode
	// for the spans alone, at the first note, and adds no tool. Default off.
	CurrentSymbol bool `toml:"current_symbol"`
	// EditTolerances lets an edit of a .go file whose old_string has no exact match apply where one tolerance places it
	// uniquely, when the file still parses (tools.Options.EditTolerances; calibration §12.22). Default off.
	EditTolerances bool `toml:"edit_tolerances"`
	// InjectContext puts the code ken finds for a session's first prompt, up to 1,600 tokens, into its first message
	// (core.Options.InjectContext; calibration §13.1). Without [ken], tack opens ken's index in bm25 mode for it, at the
	// first prompt, and adds no tool. Default off.
	InjectContext bool `toml:"inject_context"`
	// RepoMap puts a generated map of the repository, its most-referenced Go files and their exported symbols in at most
	// 3,000 bytes, into a session's first message after the instruction head (core.Options.RepoMap; calibration §13.3).
	// Without [ken], tack opens ken's index in bm25 mode for it, at the first prompt, and adds no tool. Default off.
	RepoMap bool `toml:"repo_map"`
}

// Duration is a time.Duration written as a string ("10m").
type Duration struct{ time.Duration }

// UnmarshalText implements encoding.TextUnmarshaler.
func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

// Path is the config file's location: ~/.config/tack/config.toml on every platform.
func Path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "tack", "config.toml"), nil
}

// StateDir is the platform's directory for tack's state: ~/Library/Application Support/tack on macOS,
// ~/.local/state/tack elsewhere.
func StateDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "tack"), nil
	}
	return filepath.Join(home, ".local", "state", "tack"), nil
}

// Load reads and validates the file at path, applying defaults.
func Load(path string) (*Config, error) {
	var c Config
	md, err := toml.DecodeFile(path, &c)
	if err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	if und := md.Undecoded(); len(und) > 0 {
		var keys []string
		for _, k := range und {
			keys = append(keys, k.String())
		}
		sort.Strings(keys)
		return nil, fmt.Errorf("config %s: unknown keys: %s", path, strings.Join(keys, ", "))
	}
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	return &c, nil
}

func (c *Config) validate() error {
	if len(c.Servers) == 0 {
		return errors.New("no [servers.<name>] defined")
	}
	if c.Main == "" {
		if len(c.Servers) != 1 {
			return errors.New("main is required when more than one server is defined")
		}
		for name := range c.Servers {
			c.Main = name
		}
	}
	if _, ok := c.Servers[c.Main]; !ok {
		return fmt.Errorf("main = %q names no server", c.Main)
	}
	for name, s := range c.Servers {
		s.Binary, s.ModelPath, s.TokenFile = expand(s.Binary), expand(s.ModelPath), expand(s.TokenFile)
		if (s.Temperature != nil && *s.Temperature < 0) || (s.TopP != nil && (*s.TopP <= 0 || *s.TopP > 1)) ||
			(s.TopK != nil && *s.TopK < 1) {
			return fmt.Errorf("servers.%s: temperature must be at least 0, top_p in (0, 1] and top_k at least 1", name)
		}
		switch s.Kind {
		case KindChild:
			if s.Binary == "" || s.ModelPath == "" || s.Ctx <= 0 {
				return fmt.Errorf("servers.%s: a %s needs binary, model_path and ctx", name, KindChild)
			}
			if s.URL != "" || s.TokenFile != "" {
				return fmt.Errorf("servers.%s: url and token_file are for %s", name, KindRemote)
			}
			if s.Model == "" {
				s.Model = strings.TrimSuffix(filepath.Base(s.ModelPath), ".gguf")
			}
			if s.IdleUnload.Duration == 0 {
				s.IdleUnload.Duration = 10 * time.Minute
			}
			if s.IdleExit.Duration == 0 {
				s.IdleExit.Duration = 30 * time.Minute
			}
			if s.IdleExit.Duration < s.IdleUnload.Duration {
				return fmt.Errorf("servers.%s: idle_exit is shorter than idle_unload", name)
			}
		case KindRemote:
			if s.URL == "" {
				return fmt.Errorf("servers.%s: a %s needs url", name, KindRemote)
			}
			if s.Binary != "" || s.ModelPath != "" || s.Ctx != 0 || len(s.Args) > 0 {
				return fmt.Errorf("servers.%s: binary, model_path, ctx and args are for %s", name, KindChild)
			}
		default:
			return fmt.Errorf("servers.%s: kind %q, want %q or %q", name, s.Kind, KindChild, KindRemote)
		}
		c.Servers[name] = s
	}
	if c.Run.MaxRequests == 0 {
		c.Run.MaxRequests = 40
	}
	if c.Run.MaxToolCalls == 0 {
		c.Run.MaxToolCalls = 100
	}
	if c.Run.MaxWall.Duration == 0 {
		c.Run.MaxWall.Duration = 30 * time.Minute
	}
	if c.Run.MaxTokens == 0 {
		c.Run.MaxTokens = 2048
	}
	for _, d := range []struct {
		v   *int
		def int
	}{{&c.Run.TurnTokens, 200_000}, {&c.Run.MaxErrors, 8}, {&c.Run.SessionTokens, 2_000_000}, {&c.Run.SessionToolCalls, 1000}} {
		if *d.v == 0 {
			*d.v = d.def
		}
	}
	r := c.Run
	if min(r.MaxRequests, r.MaxToolCalls, r.MaxTokens, r.TurnTokens, r.MaxErrors, r.SessionTokens, r.SessionToolCalls) < 0 ||
		r.MaxWall.Duration < 0 {
		return errors.New("run: a budget is negative")
	}
	if k := c.Ken; k != nil {
		if k.Mode == "" {
			k.Mode = "hybrid"
		}
		switch k.Mode {
		case "bm25", "semantic", "hybrid", "hybrid-rerank":
		default:
			return fmt.Errorf("ken.mode %q, want bm25, semantic, hybrid or hybrid-rerank", k.Mode)
		}
		k.ModelDir = expand(k.ModelDir)
		if k.Mode != "bm25" && k.ModelDir == "" {
			return fmt.Errorf("ken.model_dir is required for mode %s (tack passes it to ken explicitly)", k.Mode)
		}
	}
	c.StateDir = expand(c.StateDir)
	return nil
}

// expand replaces a leading ~/ with the home directory.
func expand(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	return p
}

// ReadToken reads a token file, refusing one other users can read.
func ReadToken(path string) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if st.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("%s is mode %v; a token file must be 0600", path, st.Mode().Perm())
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	tok := strings.TrimSpace(string(b))
	if tok == "" {
		return "", fmt.Errorf("%s is empty", path)
	}
	return tok, nil
}
