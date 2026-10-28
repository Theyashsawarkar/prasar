# 4. LinkedIn through an app of your own, and a 60-day sign-in

**Status:** Accepted  
**Date:** 2026-10-28

## Context

LinkedIn's API posts to a member's own profile for free through the self-serve "Share on LinkedIn" product (`w_member_social`). It needs a developer app with a registered redirect URL, and a public app shared by every prasar user would need LinkedIn's review and would put one secret in many hands. Tokens for such apps last 60 days and can't be refreshed (refresh tokens are for LinkedIn's partners).

## Decision

Each user creates their own LinkedIn app (`prasar auth linkedin --setup` walks through it) with "Share on LinkedIn" and "Sign In with LinkedIn using OpenID Connect" (to learn the member id posts are written as), and the redirect `http://localhost:8723/callback`. prasar signs in with the authorization-code flow back to that local port, keeps the token in the keyring, and shows the days left in the screen's corner, warning in the last week; `L` signs in again. Company pages (`w_organization_social`, which needs LinkedIn's review) are out of scope. The API version (`LinkedIn-Version`) is a setting, since LinkedIn retires each one after about a year.

## Consequences

- Setup takes a couple of minutes once, and every two months a sign-in in the browser.
- Each user's secret stays on their machine; nothing in prasar is shared or reviewed.
- A retired API version is fixed in config.toml, without waiting for a release.
