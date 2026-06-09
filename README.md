# claude-code-statusline (Go)

A compact status line for [Claude Code](https://code.claude.com), rewritten in Go. Single static binary, no runtime dependencies. Beyond the original Python version it pulls in **the GitLab merge request** and **the Asana task** you're working on.

```
O 4.8 1M ⚡xh | 🌿 test/e2e-screenshot-toolkit-miniapp | FTP-3853 Backlog | MR!1297 ✓ | ctx 78.0k (7%) | $2.17 · +194/-77 · 1h9m | 1.5h 22% · 2.7d 41%
```

## What it shows

| Segment | Example | Notes |
|---|---|---|
| Model + effort | `O 4.8 1M ⚡xh` | `Opus 4.8 (1M context)` → `O 4.8 1M`; reasoning effort abbreviated (`xhigh` → `xh`) |
| Git | `🌿 main*` | `*` = uncommitted changes; detached HEAD shown as `@abc1234` |
| Worktree | `🌳 agent-1 ← main` | 🌳 = linked worktree; `← main` = source branch in `--worktree` sessions |
| Multi-repo | `🌿 afisha:dev* · 🌳 cmux:agent-1` | one entry per repo when dirs are added via `/add-dir` |
| **Asana task** | `FTP-3853 Backlog` | the ticket id + board column of the task being worked on; `✓` prefix when completed; falls back to a truncated task name when there's no ticket id |
| **GitLab MR** | `MR!1297 ✓` | the open MR whose source branch is the current branch; `📝` draft · `✓`/`✗`/`●` pipeline passed/failed/running · `❗` conflicts · `💬` unresolved discussions |
| GitHub PR | `PR#12 👀` | ✅ approved · ❌ changes requested · 📝 draft · 👀 pending (from Claude Code's own `pr` field) |
| Context | `ctx 92.0k (9%)` | absolute tokens first; green <80k, yellow 80–100k, red >100k; ⚠ above 200k |
| Session | `$2.17 · +194/-77 · 1h9m` | cost, lines added/removed, duration |
| Rate limits | `1.5h 22% · 2.7d 41%` | time until the 5-hour / 7-day window resets + used %; yellow ≥70%, red ≥90% (Pro/Max only) |

Segments with no data are skipped — no empty placeholders.

## How the MR and Asana segments work (non-blocking)

A status line is re-run constantly, so it must never block on the network. This binary keeps the foreground render **local-only** (git + a small cache file) and refreshes the remote data out of band:

1. **Render** reads a cache keyed by `(repo, branch)` under `~/.cache/cc-statusline/` and prints instantly.
2. When the cache is older than 90s (or missing) it spawns a detached `--refresh` subprocess and returns immediately — the new data appears on the next render.
3. **`--refresh`** fetches the MR (via `glab`) and the Asana task (via the Asana REST API) concurrently, then writes the cache atomically. A non-blocking file lock collapses concurrent refreshes.

The very first render in a new branch shows no MR/Asana segment; they appear a second later once the background refresh lands.

## Install

Requires Go 1.23+, `git`, and (for the MR segment) the [`glab`](https://gitlab.com/gitlab-org/cli) CLI authenticated to your GitLab host.

```bash
go install github.com/ggkguelensan/claude-code-statusline@latest
# binary lands in $(go env GOPATH)/bin/claude-code-statusline
```

or build from a clone:

```bash
go build -o ~/.claude/statusline ./...
```

Add to `~/.claude/settings.json`:

```json
{
  "statusLine": {
    "type": "command",
    "command": "/Users/you/go/bin/claude-code-statusline",
    "refreshInterval": 60
  }
}
```

`refreshInterval` keeps the rate-limit countdown, the dirty indicator, and the background MR/Asana refresh ticking while the session is idle.

## GitLab MR

The MR is found automatically: `glab` resolves the host and project from the repo's `origin` remote and the binary queries the open MR whose `source_branch` is your current branch. No per-repo configuration — just make sure `glab auth status` is green for your host:

```bash
glab auth login --hostname gitlab.frhc.one
```

## Asana task

The standalone binary can't use Claude's Asana integration, so it talks to the Asana REST API with a [personal access token](https://developers.asana.com/docs/personal-access-token):

```bash
export ASANA_ACCESS_TOKEN=1/12345…   # in your shell profile
```

Without a token the Asana segment is simply skipped. With one, the task being worked on is resolved in this order:

1. **Explicit task** — `git config statusline.asana-task <gid>` (per-repo / per-worktree), or `$ASANA_TASK_GID`.
2. **Ticket id** — `git config statusline.ftp FTP-3853`, or the first `FTP-####` match in the branch name — looked up via the FTP custom field.

### Worked example (FTP-3853 / afisha !1297)

The branch `test/e2e-screenshot-toolkit-miniapp` carries no ticket id, so pin the task once in that worktree:

```bash
cd .../afisha            # on branch test/e2e-screenshot-toolkit-miniapp
git config statusline.asana-task 1215433767838047   # or: git config statusline.ftp FTP-3853
```

The MR (`!1297`) needs no config — it's detected from the branch. Result:

```
… | 🌿 test/e2e-screenshot-toolkit-miniapp | FTP-3853 Backlog | MR!1297 ✓ | …
```

### Asana config (env, with Ticketon defaults)

| Var | Default | Meaning |
|---|---|---|
| `ASANA_ACCESS_TOKEN` (`ASANA_TOKEN`, `ASANA_PAT`) | — | personal access token; required for the segment |
| `ASANA_WORKSPACE_GID` | `1208507351529750` | workspace searched for the ticket id |
| `ASANA_FTP_FIELD_GID` | `1211799464714835` | the "FTP" custom field used for ticket lookup |
| `ASANA_TICKET_PREFIX` | `FTP` | ticket prefix for branch-name extraction and the field name |

## Test without Claude Code

```bash
echo '{"model":{"display_name":"Opus 4.8 (1M context)"},"effort":{"level":"xhigh"},"workspace":{"current_dir":"."},"context_window":{"used_percentage":7,"context_window_size":1000000},"cost":{"total_cost_usd":2.17,"total_lines_added":194,"total_lines_removed":77,"total_duration_ms":4140000}}' | claude-code-statusline
```

Warm the cache manually for a given checkout (what the background refresh does):

```bash
claude-code-statusline --refresh --dir /path/to/repo --branch my-branch
```

Run the tests:

```bash
go test ./...
```

## Customizing

Each segment is a `seg*` function in `render.go` / `gitlab.go` / `asana.go`; reorder or drop the entries in `render()` (`main.go`) freely. ANSI colors live in `color.go`.

## License

[MIT](LICENSE)
