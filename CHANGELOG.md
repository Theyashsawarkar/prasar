# Changelog

Newest first. Releases are tags (`vX.Y.Z`) with `Version` in internal/app/app.go.

## v0.1.1 -- 2026-10-28

- The previews share their column by what they hold: a long X thread gets
  the room it needs (up to two thirds), so its last part is no longer cut
  off while LinkedIn's box stood half empty.

## v0.1.0 -- 2026-10-28

- First release: write once, post to X and LinkedIn from the terminal.
- **The screen**: an editor with live previews of the post as X shows it
  (X's weighted count, threads split at sentence ends or at `---` lines,
  numbered) and as LinkedIn's feed shows it (folded at "…see more"), each
  with a meter; tabs for Compose, Queue, Drafts and Posted; a key sheet
  (`?`); your varna theme's colours, live. Closing it keeps what you wrote
  as a draft.
- **LinkedIn** through its free API with an app of your own
  (`prasar auth linkedin --setup` walks through it): text with real
  hashtags, images, the 60-day sign-in counted down in the corner.
- **X with one click**, free: X's share page opens with the post filled in,
  you press Post and confirm it (pasting its link makes a thread's next part
  a reply); images go to the clipboard to paste.
- **Drafts and a schedule** (`tomorrow 9am`, `fri 6pm`, `in 2h`...), with
  `prasar service install` publishing scheduled posts in the background;
  a post is claimed before it's sent, so it never goes out twice.
- **For agents**: `prasar mcp` (`post_check`, `post_draft`,
  `post_publish`, `post_list`, `post_accounts`); every post an agent
  proposes waits for your approval in a tmux popup (`prasar approve`), where
  you can edit it.
- **CLI**: `prasar post`, `check`, `list`, `approve`, `auth`, `service`,
  `daemon`, `doctor`, `demo` (a fake LinkedIn, nothing posted).
- A TPM plugin (`prefix B`), an installer, release binaries, and a website.
