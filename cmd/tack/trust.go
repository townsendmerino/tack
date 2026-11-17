package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/townsendmerino/tack/internal/config"
	"github.com/townsendmerino/tack/internal/perms"
	"github.com/townsendmerino/tack/internal/trust"
	"github.com/townsendmerino/tack/internal/window"
)

// trustCmd is `tack trust [-yes] [path]` (DESIGN §6.6): the owner's trust in a repository's own configuration,
// .tack/config.toml. Without -yes it shows the file, escaped, and what trusting it would mean; with -yes it records the
// grant for exactly that content. A change to the file afterwards untrusts it. The grant is written to the state
// directory, which the sandbox denies to commands, so only the owner outside the sandbox can make one.
func trustCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("trust", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "config file (default ~/.config/tack/config.toml)")
	yes := fs.Bool("yes", false, "trust the repository's configuration as it is now; without it, show it")
	if err := fs.Parse(args); err != nil || fs.NArg() > 1 {
		fmt.Fprintln(stderr, "usage: tack trust [-yes] [-config FILE] [path]")
		return exitUsage
	}
	fail := func(err any) int {
		fmt.Fprintln(stderr, "tack trust:", err)
		return exitError
	}
	path := "."
	if fs.NArg() == 1 {
		path = fs.Arg(0)
	}
	root, err := trust.Canonical(path)
	if err != nil {
		return fail(err)
	}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return fail(path + " is not a directory")
	}
	repo, digest, err := trust.ReadRepo(root)
	if err == nil {
		err = perms.CheckExtraEnv(repo.Commands.Env)
	}
	if err != nil {
		return fail(err.Error() + "; nothing was trusted")
	}
	if *cfgPath == "" {
		if *cfgPath, err = config.Path(); err != nil {
			return fail(err)
		}
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return fail(err)
	}
	stateDir := cfg.StateDir
	if stateDir == "" {
		if stateDir, err = config.StateDir(); err != nil {
			return fail(err)
		}
	}
	if digest == "" {
		fmt.Fprintf(stdout, "%s has no %s: there is nothing to trust. A file added later is not trusted until you run tack trust again.\n", root, trust.ConfigPath)
	} else {
		text, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(trust.ConfigPath)))
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "%s/%s, as it is now (invisible characters shown escaped):\n\n%s\n", root, trust.ConfigPath, window.EscapeInvisible(string(text)))
		fmt.Fprintf(stdout, "Trusting it lets this repository's commands see the environment variables %v, in addition to the usual short list.\n", repo.Commands.Env)
	}
	if !*yes {
		fmt.Fprintf(stdout, "tack trust -yes %s trusts exactly this content; a change to it, by anyone, untrusts it.\n", path)
		return exitOK
	}
	if err := trust.Open(stateDir).Trust(root, digest, time.Now()); err != nil {
		return fail(err)
	}
	fmt.Fprintf(stdout, "trusted %s (config %s)\n", root, shortDigest(digest))
	return exitOK
}

func shortDigest(d string) string {
	if d == "" {
		return "none"
	}
	return d[:12]
}
