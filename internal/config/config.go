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
}

// Run holds a turn's budgets for `tack run` (DESIGN §6.3).
type Run struct {
	MaxRequests  int      `toml:"max_requests"`   // model requests per turn (default 12)
	MaxToolCalls int      `toml:"max_tool_calls"` // tool calls per turn (default 24)
	MaxWall      Duration `toml:"max_wall"`       // wall time per turn (default 10m)
	MaxTokens    int      `toml:"max_tokens"`     // output tokens per request (default 1024)
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
		c.Run.MaxRequests = 12
	}
	if c.Run.MaxToolCalls == 0 {
		c.Run.MaxToolCalls = 24
	}
	if c.Run.MaxWall.Duration == 0 {
		c.Run.MaxWall.Duration = 10 * time.Minute
	}
	if c.Run.MaxTokens == 0 {
		c.Run.MaxTokens = 1024
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
