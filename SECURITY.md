# Security policy

## Supported versions

Only the latest release of prasar gets fixes.

## Reporting a vulnerability

Please **don't open a public issue**. Report it privately through
[GitHub's security advisories](https://github.com/Theyashsawarkar/prasar/security/advisories/new),
with what an attacker could do and how to reproduce it. You'll get a reply
within a week; a fix and a release follow as soon as it's confirmed, with
credit to you unless you'd rather not.

## What prasar handles

prasar runs as your user and can post in public under your name:

- **Your LinkedIn app** (`linkedin.client_id` and `client_secret` in
  `~/.config/prasar/config.toml`, mode 0600) is your own; prasar ships none
  and the repository never contains one.
- **The LinkedIn sign-in** goes to the Secret Service keyring
  (`secret-tool`), or without a keyring to `~/.local/share/prasar/linkedin.token`
  (0600, in a 0700 folder). Sign-in is LinkedIn's authorization-code flow
  back to `localhost`, with a state check; the token is sent only to
  LinkedIn, over HTTPS, and never printed or logged.
- **X** gets no credentials at all: prasar opens X's own share page in your
  browser and you press Post there.
- **Your posts** (`~/.local/share/prasar/prasar.db`) are 0600 in a 0700 folder.

## Agents

An AI agent using `prasar mcp` can check texts, save drafts and list your
posts. **It can't publish anything by itself**: every post it asks for
waits in the "approval" state until you approve it in prasar (a tmux popup
and a notification ask you), and you can edit it first. In scope: anything
that lets an agent's post reach a network without that approval, or makes
the approval screen show something other than what is posted.
