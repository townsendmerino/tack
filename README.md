# tack

A coding agent for the Mac that runs entirely on local models: a Go daemon with a CLI and TUI, then a native macOS app,
built on [ken](https://github.com/townsendmerino/ken) for code search and
[goinfer](https://github.com/townsendmerino/goinfer) for inference.

P0 is built and P1 (the daily-driver CLI) is in progress: `tack run` answers questions about a repository and makes
edits on a local model, under a sandbox. The design is [`docs/design/DESIGN.md`](docs/design/DESIGN.md); its §16.1
says what is built so far.
