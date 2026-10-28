# 2. X through its share page, not its API

**Status:** Accepted  
**Date:** 2026-10-28

## Context

Since February 2026 X has no free API for new developers: every post made through it is paid for (about $0.01–0.015 a post, far more with a link, plus the card and taxes). prasar is meant to be free to use. Unofficial ways in (libraries that log in as you and call X's private API, or a robot clicking in your browser) break X's rules and get accounts locked.

## Decision

prasar posts to X through X's own share page, `https://x.com/intent/post?text=…`: it opens the page in your browser with the post filled in and you press Post. Then you tell prasar it's done (pasting the post's link if you have it). A thread opens one part at a time, each part a reply to the one before when you gave its link (`in_reply_to`). The share page takes no images, so prasar puts the first one on the clipboard to paste. A scheduled X post can't go out on its own: at its time a notification offers to open it.

## Consequences

- Posting to X is free and within X's rules, and prasar holds no X credentials at all.
- X always needs your click, so prasar can't confirm an X post by itself: it trusts your "posted", and an X link is known only if you paste it.
- The code keeps X behind one function (`openX`), so posting through the paid API could be added later as an option, without changing the rest.
