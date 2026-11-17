// Package trust is the owner's trust in a repository's own configuration (DESIGN §6.6, "A repo's own configuration is
// untrusted until the owner trusts the repo"). A repository's .tack/config.toml takes effect in a session only while the
// owner's grant for that repository names the file's exact content: a changed file, or one that was not there, is
// untrusted until the owner grants again.
//
// The grants live in the state directory, which the sandbox denies to commands, so a command cannot trust its own
// repository. Making a grant is the privileged act: tack trust, and the workspace/trust method, which needs the client token.
package trust

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
)

// ConfigPath is a repository's own configuration, relative to its root.
const ConfigPath = ".tack/config.toml"

// maxConfig is the largest repository configuration read. A larger file is an error, not a prefix.
const maxConfig = 64 << 10

// Repo is a repository's .tack/config.toml. Every setting is a field, and an unknown key is an error, so a typo fails
// loudly (CLAUDE.md: settings are config).
type Repo struct {
	Commands Commands `toml:"commands"`
}

// Commands is [commands]: what the repository's commands are given.
type Commands struct {
	// Env names variables of tack's own environment that reach the repository's commands, beyond the built-in list
	// (perms.CommandEnvAllowed). One named like a secret never passes, from here or anywhere.
	Env []string `toml:"env"`
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ReadRepo reads root's .tack/config.toml. It returns the file's configuration and the digest of its exact bytes (SHA-256,
// hex), or the zero Repo and "" when the file is not there. A file that is not a regular file, is over 64 KiB, does not
// parse, holds a key that is not a setting or names something that is not a variable name is an error; the digest is
// still returned when the bytes were read, so a caller can tell a changed file from a broken one.
func ReadRepo(root string) (Repo, string, error) {
	p := filepath.Join(root, filepath.FromSlash(ConfigPath))
	fi, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return Repo{}, "", nil
	}
	if err != nil {
		return Repo{}, "", err
	}
	if !fi.Mode().IsRegular() {
		return Repo{}, "", fmt.Errorf("%s is not a regular file", ConfigPath)
	}
	if fi.Size() > maxConfig {
		return Repo{}, "", fmt.Errorf("%s is over %d bytes", ConfigPath, maxConfig)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return Repo{}, "", err
	}
	sum := sha256.Sum256(b)
	digest := hex.EncodeToString(sum[:])
	var r Repo
	md, err := toml.Decode(string(b), &r)
	if err != nil {
		return Repo{}, digest, fmt.Errorf("%s: %w", ConfigPath, err)
	}
	if und := md.Undecoded(); len(und) > 0 {
		return Repo{}, digest, fmt.Errorf("%s: unknown setting %s", ConfigPath, und[0])
	}
	for _, n := range r.Commands.Env {
		if !envName.MatchString(n) {
			return Repo{}, digest, fmt.Errorf("%s: [commands] env: %q is not a variable name", ConfigPath, n)
		}
	}
	return r, digest, nil
}

// Grant is the owner's trust in one repository: the digest of the configuration file he read ("" for none) and when.
type Grant struct {
	Config string    `json:"config"`
	At     time.Time `json:"at"`
}

// Store is the grants, one file in the state directory. It is safe for concurrent use in one process.
type Store struct {
	path string
	mu   sync.Mutex
}

// Open returns the store in stateDir. Nothing is read or written until it is used.
func Open(stateDir string) *Store { return &Store{path: filepath.Join(stateDir, "trust.json")} }

// Canonical is root as a grant is keyed: absolute, with symbolic links resolved.
func Canonical(root string) (string, error) {
	a, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(a)
}

func (s *Store) read() (map[string]Grant, error) {
	b, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]Grant{}, nil
	}
	if err != nil {
		return nil, err
	}
	m := map[string]Grant{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("trust: %s: %w", s.path, err)
	}
	return m, nil
}

// Lookup is the grant for root, and whether there is one. A store that cannot be read, or holds nothing it understands,
// is an error: no grant is assumed.
func (s *Store) Lookup(root string) (Grant, bool, error) {
	key, err := Canonical(root)
	if err != nil {
		return Grant{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.read()
	if err != nil {
		return Grant{}, false, err
	}
	g, ok := m[key]
	return g, ok, nil
}

// Trust records the owner's grant for root: its configuration, whose digest is config, as of now. It replaces any
// earlier grant for the repository. The file is written whole and renamed into place, owner-only.
func (s *Store) Trust(root, config string, now time.Time) error {
	key, err := Canonical(root)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.read()
	if err != nil {
		return err
	}
	m[key] = Grant{Config: config, At: now.UTC()}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".trust-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

// Status is where a repository's own configuration stands for a session starting in it.
type Status struct {
	// Applied says the configuration is in effect: it is there, and the owner's grant names exactly this content.
	Applied bool
	Repo    Repo   // the configuration, when Applied; the zero Repo otherwise
	Digest  string // the file's digest, "" when there is no file
	// Notice is what to tell the owner when the file is there and is not applied, and why. "" when it is applied or there is none.
	Notice string
}

// Check is where root's .tack/config.toml stands against the store. It never applies a file the owner has not trusted: a
// file that is not there needs nothing; one with no grant, a grant for other content, or a store that cannot be read is
// not applied, and the Notice says why and what to run.
func (s *Store) Check(root string) Status {
	repo, digest, rerr := ReadRepo(root)
	if rerr == nil && digest == "" {
		return Status{}
	}
	st := Status{Digest: digest}
	g, ok, err := s.Lookup(root)
	switch {
	case err != nil:
		st.Notice = ConfigPath + " is not applied: the trust grants cannot be read (" + err.Error() + ")"
	case !ok:
		st.Notice = ConfigPath + " is not applied: this repository is not trusted. Read it, then run: tack trust " + root
	case g.Config != digest:
		st.Notice = ConfigPath + " is not applied: it changed since you trusted this repository. Read it, then run: tack trust " + root
	case rerr != nil:
		st.Notice = ConfigPath + " is not applied: " + rerr.Error()
	default:
		st.Applied, st.Repo = true, repo
	}
	return st
}
