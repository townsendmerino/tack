# docs/measurements

Dated measurement records: E0 model surveys, E1 runs, pre-registrations. Each record carries machine, model and quant,
server and version, commit, date, and the rule it was graded against. Raw data over ~1 MB is committed as `.gz`.

**ken version.** Runs from the commit that pins it (2026-11-10) use ken v1.14.0; earlier runs used the version their
record names (v1.13.0 just before). From v1.14.0 ken's engine reads no environment variable, so a `KEN_*` variable in a
run's environment does not reach tack's index. A measured run sets no parse budget (`docs/design/DESIGN.md` §12).
