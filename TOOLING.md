# TOOLING.md

Authoritative developer tooling for this project. Any automation, scripts, or workflow the agent creates must use these tools — nothing else.

## Make

- **Make is the single task runner.** All repeatable commands (build, run, test, lint, migrate, dev servers, proxy) belong in a `Makefile` at the repo root.
- Do not create standalone shell scripts, npm scripts for orchestration, or ad-hoc task runners (just, task, etc.). If a command is worth repeating, add a Make target.
- Targets should be short, verb-based, and documented: each target gets a `## comment` so `make help` can list them.
- Convention:
  - `make dev` — start the dev environment (apps + proxy)
  - `make build` — build all apps and the Go backend
  - `make test` — run all tests
  - `make lint` — lint frontend and backend
  - `make migrate` — apply database migrations
- **Never run `next build` by hand.** `make build` sets `NEXT_DIST_DIR=.next-build` for every workspace; a plain `next build` writes into `.next` and clobbers the running dev servers (404 chunks, 500s). Recovering from that is `make apps-restart`, which restarts only the three Next.js dev servers and leaves the proxy, ngrok and the API alone.
- Prefer one root Makefile that delegates into `apps/*` and `proxy/` over per-directory Makefiles.

## Git

- **Git is the only version control tool.** No alternative VCS, no tools that bypass git history.
- Workflow: feature branches off `main`, merge back to `main`. Do not commit directly to `main` for non-trivial changes.
- Commits: small, focused, imperative-mood messages (e.g. `add tenant auth middleware`).
- Never rewrite published history (`push --force` to shared branches).
- Generated files, `node_modules/`, build output, and secrets stay out of git — keep `.gitignore` current.

## Rules for the agent

- Before writing any script or automation, check whether a Make target already covers it; extend the Makefile rather than duplicating.
- New tooling (formatters, linters, migration tools) must be wired through Make targets so there is one way to run everything.
- If a task genuinely cannot be done with Make + Git, update this file first, then implement — same rule as TECHSTACK.md.
