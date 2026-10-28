# 3. An agent's post always waits for your approval

**Status:** Accepted  
**Date:** 2026-10-28

## Context

`prasar mcp` lets AI agents write posts for you. A post is public, under your name, and can't really be taken back once people have seen it. sandesh (mail) lets an agent send to people you've written to before without asking; nothing like that "safe audience" exists for a public post.

## Decision

Every post an agent asks to publish is stored as `approval`, and nothing moves it on but you: prasar opens a tmux popup on the agent's pane (`prasar approve <id>`) and sends a notification; the post also waits in the Queue tab. The approval screen shows the post as each network will show it, the agent's one-line reason, and lets you edit before approving or decline. Approving runs exactly what the screen shows. Agents may freely check texts, save drafts and list posts, which publish nothing.

## Consequences

- An agent can never post on its own, even scheduled: a scheduled agent post is approved first, then waits for its time.
- The MCP call waits up to 10 minutes for your answer, then reports that it's still waiting, never that it posted.
- Edits made while approving are reported back to the agent with the text that went out.
