# prasar -- notes for agents

Write once, post to X and LinkedIn: one Go binary (`cmd/prasar`) with a
Bubble Tea screen, a CLI, an MCP server and a background service for the
schedule, all over one SQLite file. Usable as the `prasar` command
(install.sh, make install, kosha) and as a TPM plugin (`prasar.tmux` binds
a popup key). It is pulled into Vayu (github.com/Theyashsawarkar/vayu) as a
tmux plugin, but must work on any Linux setup without Vayu: no hardcoded
paths, Vayu-specific behaviour only through the `@prasar-*` options and
config.toml.

- Layout: `internal/post` each network's rules (X's weighted count and
  threads, LinkedIn's little-text escaping); `internal/app` the core every
  front end shares (config, the posts' lifecycle, publishing, the
  schedule); `internal/store` SQLite; `internal/linkedin` sign-in and the
  API; `internal/fake` a fake LinkedIn for tests and `prasar demo`;
  `internal/tui` the screen; `internal/mcpserver` the agent tools;
  `internal/ui` the drawing kit shared in look with sandesh, pattan and
  nauka; `internal/when` the time parser (sandesh's).
- **It must stay free**: X goes through its share page, never its paid API
  or anything that logs in as the user (ADR 2). LinkedIn through the user's
  own app (ADR 4).
- **Agents never post on their own**: an agent's post is `approval` until
  the user approves it (ADR 3). Never add a path around that. Status
  changes go through `Store.Transition` with the expected old status.
- Keys live in `internal/tui/keys.go`. After adding, changing or removing
  one: `go test ./internal/tui -update` (rewrites `scripts/keybindings`,
  which Vayu's keybinding search reads), and update the README's key
  tables (a test checks them). Descriptions start with a verb the search
  knows.
- The README documents every feature, key, option and command: a change to
  any of them updates it. Every change gets a CHANGELOG.md entry; releases
  are tags (`vX.Y.Z`) with `Version` in internal/app/app.go bumped to match.
- `make check` must pass (vet, gofmt, ShellCheck, tests).
- Test against the fake LinkedIn (`prasar demo`, `PRASAR_HOME`,
  `PRASAR_LINKEDIN_URL`), never the user's real accounts, and never open
  X's share page from a test. Drive the screen in a throwaway tmux server:
  `tmux -L t -f /dev/null new -d -s o -x 150 -y 40 "env -u TMUX bin/prasar demo"`,
  then `tmux -L t send-keys -t o ...` / `capture-pane -p -t o`, and
  `tmux -L t kill-server` after. Send Esc on its own (Esc then a key at once
  reads as Alt+key).
- The website (docs/, GitHub Pages) is generated: after changing README.md,
  CHANGELOG.md or docs/adr/, run `python3 scripts/build-site.py` and commit
  docs/ (CI checks it). A significant design decision gets a new ADR in
  docs/adr/ (next number; supersede, never rewrite).
