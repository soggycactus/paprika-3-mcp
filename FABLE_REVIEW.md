# FABLE_REVIEW.md — paprika-3-mcp fork review

*Researched and written 2026-06-09. Reviewer: Claude (Fable 5), acting as senior Go engineer.*

## Summary

This repo is `bsitkoff/paprika-3-mcp` (branch `add-meal-grocery-features`), a fork of `soggycactus/paprika-3-mcp` — a Go MCP server for Paprika 3. The fork adds meal-planning and grocery tools on top of upstream's recipe tools, implemented via **V1 endpoints with Basic Auth** (`POST /api/v1/sync/meals/`, `/api/v1/sync/groceries/`). Meal creation now fails because **those V1 endpoints return 401** — the V1-Basic-Auth approach is itself the thing that broke.

State of the upstream: **frozen and effectively orphaned**. Upstream `main` is still at `c8de3bf` (Apr 13, 2025) — exactly the commit this fork is based on. The maintainer opened issue #13 *"New maintainer(s) needed"* (May 28, 2026). The only upstream branch with newer work is `lucas/fixes` (PR #5, Dec 2025, unmerged).

Severity of divergence: **low in git terms** (fork = upstream main + 2 commits + 1 uncommitted change; upstream hasn't moved at all), but **high in community terms** — the fork's feature work was picked up, improved, fixed, and tested by other forks, and the current best version of this codebase lives in an open upstream PR, not here.

**Bottom line: the fix already exists.** Upstream PR #11 (JoshTerAvest, Mar 30, 2026) switches meal/grocery writes to the V2 bulk endpoints with Bearer auth and is confirmed working by two independent users. Recommendation: adopt that branch rather than patch this one (details below).

## Research findings

### Upstream (`soggycactus/paprika-3-mcp`)

- **Commits:** none since `c8de3bf` (Apr 2025). No development has happened on `main` since this fork was created. 17 forks, 11 open issues/PRs.
- **Issue #13** (May 2026): maintainer (soggycactus) is seeking new maintainers. JoshTerAvest has volunteered (comment on PR #11).
- **Branch `lucas/fixes` / PR #5** (soggycactus, Dec 2025, open): upgrades `mark3labs/mcp-go` v0.18.0 → v0.43.2, replaces raw `req.Params.Arguments["x"].(string)` type assertions with `req.RequireString(...)` and `mcp.NewToolResultError(...)`. Fixes the nil-interface panic in issue #4. Does **not** touch meals/groceries (upstream never had them).

### Has the V1/V2 meal API issue been addressed? Yes — PR #11

The history is a relay race that started from this very fork:

1. **PR #3** (bsitkoff — this fork, Oct 2025, open): "Add meal planning and grocery management support" — the V1 Basic Auth implementation that is now broken.
2. **PR #10** (ksletmoe, Mar 24, 2026, closed): *explicitly "Based on PR #3"* — kept the features, added a `PaprikaClient` interface, 22 mock-based unit tests, `postV2` helper, multi-grocery-list support, and fixed two latent bugs (date-filter boundary, missing soft-delete filtering). Still used V1 for writes.
3. **In PR #10's discussion, JoshTerAvest reported the V1 endpoints returning 401** with *both* Basic Auth and Bearer token, and found the fix: the **V2 bulk array endpoints**. ksletmoe tested it: *"I tested this out this evening while meal planning and your branch all works great"*, then closed #10 in favor of:
4. **PR #11** (JoshTerAvest, Mar 30, 2026, **open** — head `JoshTerAvest:main`): "Fix meal and grocery sync: use V2 bulk endpoints". The confirmed-working write path is:
   - `POST https://www.paprikaapp.com/api/v2/sync/meals/` and `POST https://www.paprikaapp.com/api/v2/sync/groceries/`
   - body: **gzipped JSON array** (even for one item) in a multipart form field `data`
   - auth: **Bearer token** (same as recipe endpoints) — *not* Basic Auth
   - host: **`www.paprikaapp.com`** (the missing `www.` may have contributed to the 401s)
   - deletes remain soft (`deleted: true` in the same array POST)

PR #11 also bundles: shared `postV2` helper (removes ~400 lines of duplicated gzip/multipart code), a retry transport (429/5xx with `Retry-After` support), the `Categories: nil → []` fix for issue #7 ("Value cannot be null. Parameter name: collection."), `list_recipes` / `delete_recipe` / `list_grocery_lists` tools, concurrent recipe fetching for search, and the PR #10 test suite. It's unmerged only because upstream is unmaintained.

### Other forks

Of 17 forks, the only ones with meaningful development are the chain above: `ksletmoe/paprika-3-mcp` (PR #10) and `JoshTerAvest/paprika-3-mcp` (PR #11 + PR #12, last pushed May 26, 2026). The rest are stale copies of upstream or this fork's own (`bsitkoff`).

### Is "meals require V1 since Sept 2024" still true? The premise is garbled — here's the corrected history.

A full sweep of community documentation (the canonical mattdsteele gist + its comment thread, the kappari reverse-engineering project, the Home Assistant integration thread, several open-source clients) establishes:

- **What actually broke in September 2024 was the V2 *login* endpoint, not meal sync.** `POST /api/v2/account/login` started rejecting password-only logins with "Unrecognized client" (~Sept 19, 2024, multi-source). Around the same time Paprika introduced **aggressive rate limiting / temporary IP bans** for heavy callers (~hundreds of calls/min). Previously issued tokens kept working.
- **There is no public evidence a V2 meals write ever worked before 2026** — the "V2 meal endpoints broke" framing is unverifiable. The official client uses `/api/v2/sync/menuitems/` for the separate *Menus* feature; for the meal *planner*, the only public worked example (gist commenter dahoo, Jul 2025) is `POST https://www.paprikaapp.com/api/v1/sync/meals/` with a gzipped JSON array in multipart field `data` — exactly the seven fields this fork sends (`uid, recipe_uid, date, type, name, deleted, order_flag`).
- **A token from `POST /api/v1/account/login/` works as a Bearer token on V2 sync endpoints** (multi-source, freshest data point May 31, 2026 via kappari). This is exactly what this codebase's `login()` already does — so the Bearer-on-V2 write path in PR #11 is consistent with the documented auth model.
- **No breaking API changes were found in 2025-2026.** V1 Basic Auth writes were still being reported working as late as Jul 2025 (dahoo) and the V1-login/V2-sync pattern as late as May 2026 (kappari).

So the honest reconciliation: community docs say V1 Basic Auth meal writes *should* still work (and did for PR #10's author — JoshTerAvest allowed his 401s "might be account-specific"), yet this fork fails for its owner and JoshTerAvest's V1 attempts failed with both auth schemes, while the V2-bulk path was verified working by two people against this codebase in March 2026. One concrete difference from every *working* example: this fork posts to **`paprikaapp.com`** while both dahoo's working V1 example and PR #11's working V2 code use **`www.paprikaapp.com`**. (A cross-host redirect would make Go's `net/http` strip the `Authorization` header — but a credential-less probe today shows no redirect on either host, so that can't be confirmed from outside.) Another possibility is the Sept-2024 rate limiter: this fork's resource loop re-fetches every recipe every minute, which on a large collection approaches the reported ban threshold, and Paprika's blocks are IP-scoped.

The recommendation is unaffected: PR #11's path is the one most recently verified against this exact codebase, and it's consistent with the documented auth model. The first verification step after adopting it must be an end-to-end test against *this* account — and if that somehow fails, the documented fallback is dahoo's V1 recipe with the `www.` host.

Key sources (full list and confidence levels in the research agent's notes):
- Canonical API gist + comments (Sept-2024 timeline, meal POST format): https://gist.github.com/mattdsteele/7386ec363badfdeaad05a418b9a1f30a
- kappari — actively maintained reverse-engineering of the V2 API/auth, updated May 2026: https://github.com/johnwbyrd/kappari
- Home Assistant thread (independent confirmation of the login break + V2 reads): https://community.home-assistant.io/t/paprika-recipe-app-integration-whats-for-dinner-tonight/707405
- joshstrange/paprika-api (V1 Basic Auth GET catalog): https://github.com/joshstrange/paprika-api

## Decision: rebase vs. fix in place

**Recommendation: adopt PR #11's branch (`JoshTerAvest:main`) and re-apply this fork's one unique improvement on top.**

Reasoning:

- A literal "rebase from upstream" is moot — upstream `main` has not moved since the fork point. There is nothing to rebase onto.
- The real choice is (a) port the endpoint/auth fix into this fork's code, or (b) take PR #11's branch wholesale. Option (a) is only ~20 lines of semantic change, **but** it would be spread across four near-identical ~100-line functions, and it would re-derive by hand what PR #11 already has tested and reviewed. Option (b) gets the identical fix **plus** 22 unit tests, the deduplicated `postV2` helper, retry logic, the issue-#7 recipe fix, and bug fixes to date filtering and soft-delete handling — and loses nothing, because PR #11 descends from this fork's own PR #3 feature set.
- The only local work not in PR #11 is the uncommitted `cmd/paprika-3-mcp/main.go` change (env-var fallback for `--username`/`--password` — worth keeping; it's a one-commit cherry-pick).
- Deliberately deferred: PR #5's mcp-go v0.43 upgrade. It's good hygiene but orthogonal to the breakage, and it churns every tool handler. Do it as a follow-up, not bundled with the fix.

## Implementation plan

1. **Preserve local work:** commit the pending `main.go` change on `add-meal-grocery-features`
   (`git add cmd/paprika-3-mcp/main.go && git commit -m "Add env var fallback for credentials"`).
2. **Fetch the fixed branch:**
   `git remote add josh https://github.com/JoshTerAvest/paprika-3-mcp.git && git fetch josh`
3. **Create the adoption branch:** `git checkout -b adopt-v2-bulk-fix josh/main`
4. **Cherry-pick the env-var commit** from step 1 (`git cherry-pick <sha>`; conflict risk is near zero — PR #11 doesn't touch `main.go` beyond flag help text; if both touched it, keep both changes).
5. **Apply PR #8's login fix** (closed-unmerged upstream, but correct): in `internal/paprika/client.go` `login()`, replace
   `body := fmt.Sprintf("email=%s&password=%s", username, password)` with
   `body := url.Values{"email": {username}, "password": {password}}.Encode()` (+ `net/url` import). Optionally bring over its `login_test.go`.
6. **Check PR #12** (Categories nil normalization) — PR #11's diff already contains the `SaveRecipe` normalization; verify after checkout and skip #12 if present.
7. **Update the docs that encode the wrong API model:** `CLAUDE.md` and `WARP.md` both teach "V1 (Basic Auth) required for meal/grocery writes" as fact. Rewrite those sections to the V2-bulk truth, or a future session will confidently "fix" the code backwards.
8. **Verify:**
   - `go build ./... && go vet ./... && go test ./...` (PR #11's unit tests run without credentials)
   - `PAPRIKA_USERNAME=… PAPRIKA_PASSWORD=… go test ./internal/paprika -v` (integration; mutates the real account)
   - End-to-end: `make install`, restart the MCP server, `add_meal_to_plan` for a test date, confirm via `list_meal_plan` and in the Paprika app, then `remove_meal_from_plan`.
   - **Contingency:** if the V2-bulk write fails against this account too, fall back to the community-documented V1 form with the corrected host — `POST https://www.paprikaapp.com/api/v1/sync/meals/`, Basic Auth, same gzipped-array payload (the dahoo/Jul-2025 recipe). The delta from current code is then just the `www.` prefix.
9. **Housekeeping:** push to the `bsitkoff` fork; comment on upstream PR #3 pointing to PR #11 as its successor (optionally close #3); consider responding to issue #13 — the people who fixed this are actively offering to maintain.

## Go code quality

The original upstream core (client.go recipe paths, server scaffolding) is serviceable hobby-grade Go. The meal/grocery additions in this fork have the hallmarks of LLM-assisted bolt-on code. Specific winces, current code:

- **Massive duplication:** `SaveMealPlan`, `DeleteMealPlan`, `SaveGroceryItem`, `DeleteGroceryItem` (`internal/paprika/client.go:643-1035`) are four copies of the same ~95-line gzip→multipart→POST→check sequence, differing only in URL and payload. `SaveRecipe` is a fifth variant. PR #11's `postV2(ctx, endpoint, v interface{})` collapses all of them.
- **No-op code:** `if item.OrderFlag == 0 { item.OrderFlag = 0 }` (`client.go:845-847`).
- **Broken error capture in goroutines:** `addResourcesConcurrently` (`internal/mcpserver/server.go:216-229`) declares `go func() (err error)` (a return value nobody receives) and registers `defer func(err error){...}(err)` — the deferred closure receives `err`'s value *at defer time*, which is always nil, so failures are silently swallowed. There's also no `WaitGroup`, so shutdown ordering is luck.
- **Login encoding bug:** `login()` (`client.go:117`) builds the form body with `fmt.Sprintf` — a password containing `&`, `=`, `+`, or `%` corrupts the request. Fixed (with table-driven tests) in upstream PR #8.
- **Hand-rolled bubble sorts** for date and aisle keys (`server.go:806-812`, `server.go:904-910`) where `sort.Strings(dates)` is one line.
- **Date-boundary bug:** `list_meal_plan` compares `meal.Date` (`"YYYY-MM-DD HH:MM:SS"`) lexically against `end_date` (`"YYYY-MM-DD"`); `"2026-06-09 00:00:00" > "2026-06-09"`, so meals on the end date are excluded. (PR #10/#11 fix this.)
- **Soft-deleted rows not filtered:** `listMealPlan` / `listGroceries` render items even when `Deleted: true`, so "removed" entries can reappear in tool output.
- **N+1 serial fetch:** `searchRecipes` calls `GetRecipe` sequentially for every recipe in the account inside a 30s timeout; large collections will time out. (PR #11 fetches concurrently with a semaphore.)
- **Stale framework + error convention:** `mcp-go v0.18.0` (current: v0.43.x). Handlers return Go errors for bad arguments (a *protocol* error to the client) instead of `mcp.NewToolResultError` (a *tool* error the model can react to). Upstream PR #5 shows the exact migration.
- **Dead field:** `NewServerOptions.Paprika` (`server.go:20`) is never used — `NewServer` always constructs its own client.
- **Testing:** the only test is one integration test that creates/deletes real recipes in a live account and requires credentials; nothing is unit-testable because `*paprika.Client` is concrete. PR #11's `PaprikaClient` interface + mock tests fix this.
- Minor: hashing pre-sorts map keys before `json.Marshal`, which already sorts map keys — harmless but redundant; `notify()` is deferred with a context that may be near its deadline; stray double blank lines.

## Additional findings

- **Rate-limiter exposure:** since ~Sept 2024 Paprika temporarily IP-bans heavy callers (reported trigger: hundreds of calls/min; the official app makes 2-4 per session). This server's `updateResources` loop re-fetches **every recipe individually every minute**, and `search_recipes` re-fetches the whole collection per query. On a large recipe collection this flirts with the ban threshold — and a ban would also break the Paprika apps on the same network. `ListRecipes` already returns a per-recipe `hash`; cache by hash and skip unchanged recipes, and/or lengthen the refresh interval substantially.
- **The docs are a footgun:** `CLAUDE.md` and `WARP.md` both assert the V1-Basic-Auth model ("V1 API … required for creating/updating meal plans") as established fact, citing the Kappari docs. Any agent or human following them will reintroduce the broken pattern. Updating them is part of the fix, not optional polish.
- **Uncommitted work:** `cmd/paprika-3-mcp/main.go` has a good, unrelated improvement sitting uncommitted (env-var fallback + clearer error message). Commit it before any branch surgery.
- **Maintenance opportunity:** upstream is explicitly seeking maintainers (issue #13) and JoshTerAvest has volunteered. This fork's author started the meal/grocery feature chain (PR #3 → #10 → #11); coordinating there (rather than continuing a private fork) would get the fix merged and released via the existing Homebrew tap.
- **Module path:** the fork still declares `module github.com/soggycactus/paprika-3-mcp`. Fine while it's a faithful fork, but worth renaming if it's going to diverge long-term.
- **Issue #7** ("Value cannot be null. Parameter name: collection.") affects this fork too: `create_paprika_recipe` doesn't set `Categories`, a nil slice marshals to `null`, and Paprika's .NET clients choke on it during sync. PR #11/#12 normalize `nil → []` inside `SaveRecipe`.
