# `aispend` — Incremental Build Plan (run-by-run)

**Companion to:** `aispend-cli-design.md` (the design is the spec; this file is the execution order)
**Rule:** every run ends with a binary that builds and a command you can run and eyeball.
**Contract per run:** I implement one slice → you run the `Verify` block → you tell me pass/fail → next run.

Project lives in `ai-gov/aispend/`. Module path is `github.com/prabhuvmk/aispend` (placeholder — say the word and I'll rewrite it in one pass).
Binary name `aispend` is also a placeholder per §0 of the design.

---

## Ground rules carried through every run

- `make dev ARGS="..."` rebuilds and runs. One keystroke.
- `go build ./...` and `go vet ./...` must pass at the end of every run. If they don't, the run isn't done.
- Global `--debug` exists from run 1.
- No dependency is added unless it is on the §2.4 list: cobra, modernc.org/sqlite, go-keyring, go-pretty, x/term. Nothing else.
- Money is `int64` micros. Never a float, in any run.
- Dates are UTC `YYYY-MM-DD` strings. Converted only at display time.
- One commit per run (I'll leave the commit to you unless you ask me to make it).

---

## Run map

| Run | Slice | Design steps | Est. |
|---|---|---|---|
| 1 | Skeleton: module, Makefile, cobra root, `version`, `--debug` | 1 | 30m |
| 2 | Config dir + `doctor` (paths only) | 2 | 30m |
| 3 | Embedded catalog + `connections` table output | 3 | 45m |
| 4 | SQLite open + migrations + `doctor` db health | 4 | 1h |
| 5 | `Sink` interface + `SQLiteSink` + `debug seed` | 5 | 45m |
| 6 | Credential resolver (env only) + `connections` shows keys | 6 | 45m |
| 7 | Egress-guarded HTTP client + `scan --dry-run` + the guard test | 7 | 1h |
| 8 | Fixture mode (moved early, deliberately) | 15 | 45m |
| 9 | OpenAI `Verify()` — first real network call | 8 | 1h |
| 10 | OpenAI `Collect()` one day, print facts only | 9 | 2h |
| 11 | Persist through the sink | 10 | 45m |
| 12 | Backfill, cursors, resume | 11 | 1.5h |
| 13 | `fmtutil` + `usage` total-only report | 12, 13 | 1.5h |
| 14 | `BY VENDOR` table | 14 | 1h |
| 15 | Anthropic collector | 16 | 2h |
| 16 | Price book, `amount_basis`, Basis footer | 17 | 1.5h |
| 17 | `BY MODEL` table | 18 | 1h |
| 18 | Sparklines + prior-window deltas + ASCII fallback | 19 | 1.5h |
| 19 | OpenRouter collector | 20 | 1.5h |
| 20 | `owners.csv` + `--by team` + Unattributed line | 21 | 2h |
| 21 | Surprise rules + `⚠` block | 22 | 2h |
| 22 | `--json` / `--csv` / `export` | 23 | 1h |
| 23 | `connect` (masked input + keyring), `disconnect`, `purge` | 24, 25 | 2.5h |
| 24 | `export --share` | 26 | 45m |
| 25 | Error-message pass + redacting writer test | 27 | 1h |
| 26 | README (security first) + release build | 28, 29 | 4h |

**Deviation from the design doc, and why:** fixture mode moves from step 15 to run 8, before the first
collector is written. The design already calls it "worth every minute"; putting it *before* the OpenAI
collector means every collector is written against a deterministic fixture first and the network call is
just a swap of the transport. It also means runs 9–22 are verifiable by you without any API key or spend.

**If we fall behind, cut in this order:** run 19 (OpenRouter) → run 21 (surprises) → the `--csv` half of
run 22. Never cut runs 7, 8, 20, 25.

---

## Run detail

Each run below states: what gets built, what files appear, and exactly what you type to verify.

### Run 1 — walking skeleton
**Build:** `go.mod`, `Makefile`, `main.go`, cobra root command with `--debug` and `--no-color` persistent
flags, `version` subcommand fed by `-ldflags`.
**Files:** `main.go`, `Makefile`, `internal/cli/{root,version}.go`, `internal/buildinfo/buildinfo.go`, `.gitignore`
**Verify:**
```bash
make build                 # compiles clean
make dev ARGS=""           # help text, lists commands, shows --debug
make dev ARGS="version"    # aispend 0.1.0-dev (<sha>, go1.26.0, darwin/arm64)
make dev ARGS="version --debug"   # same + a debug line proving the flag is wired
```
**Done when:** all four print, `go vet ./...` is silent.

### Run 2 — config paths and `doctor`
**Build:** `~/.aispend/` created at `0700` on demand, `AISPEND_HOME` override for testing, `doctor` printing
the paths block only.
**Verify:**
```bash
make dev ARGS="doctor"
# paths   config  ~/.aispend/            (0700 ✓)
#         db      ~/.aispend/aispend.db  (missing)
ls -ld ~/.aispend        # drwx------
AISPEND_HOME=/tmp/as1 ./aispend doctor && ls -ld /tmp/as1
```
**Done when:** perms are `0700`, and the override lands elsewhere so tests never touch your real home.

### Run 3 — catalog and the first table
**Build:** `catalog.json` embedded via `//go:embed`, three vendors with endpoints + allowed hosts + rate
limits; `connections` (alias `ls`) rendering it; the colour/TTY decision made once here.
**Verify:**
```bash
make dev ARGS="connections"
# VENDOR       UNIT    STATUS
# openai       token   not connected
make dev ARGS="connections" | cat        # no ANSI codes when piped
NO_COLOR=1 make dev ARGS="connections"   # no colour
```
**Done when:** piping produces clean ASCII-safe text with no escape codes.

### Run 4 — SQLite and migrations
**Build:** `modernc.org/sqlite`, schema v1 exactly as §5, migration runner keyed on `meta.schema_version`,
`doctor` gains a db line.
**Verify:**
```bash
make dev ARGS="doctor"
# db      ok · schema v1 · 0 facts · 0 connections
sqlite3 ~/.aispend/aispend.db ".tables"     # connection meta sync_state usage_fact
ls -l ~/.aispend/aispend.db                 # -rw-------
make dev ARGS="doctor"                      # second run: no re-migration, same output
```

### Run 5 — sink interface and a fake
**Build:** `Sink` interface (§9.2), `SQLiteSink`, hidden `debug seed` writing 3 synthetic facts in one tx.
**Verify:**
```bash
make dev ARGS="debug seed" && make dev ARGS="doctor"
# db  ok · schema v1 · 3 facts
make dev ARGS="debug seed" && make dev ARGS="doctor"
# still 3 facts — idempotency via the primary key, not a bug
sqlite3 ~/.aispend/aispend.db "select vendor,day,model_ref,amount_micros from usage_fact;"
```

### Run 6 — credential resolver (env only)
**Build:** env → keychain lookup order with keychain stubbed out this run, truncated display (`sk-…a4f2`),
resolver never touches SQLite.
**Verify:**
```bash
OPENAI_ADMIN_KEY=sk-test-000000000000a4f2 make dev ARGS="connections"
# openai   token   key in env OPENAI_ADMIN_KEY (sk-…a4f2)
make dev ARGS="connections"   # openai back to "not connected"
grep -ri "OPENAI_ADMIN_KEY" ~/.aispend/aispend.db   # no match, ever
```

### Run 7 — egress guard
**Build:** `http.Transport` with the catalog-checking `DialContext` from §8, `scan --dry-run`, and the test
that a non-catalog host is refused. This test *is* the security claim.
**Verify:**
```bash
make dev ARGS="scan --dry-run"
# would GET api.openai.com/v1/organization/usage/completions ×30
go test ./internal/egress/... -v      # TestBlocksUnlistedHost passes
```

### Run 8 — fixture mode
**Build:** `--fixture testdata/` swaps the transport for one serving canned JSON; fixtures for the OpenAI
usage endpoint checked in.
**Verify:**
```bash
make dev ARGS="scan --fixture testdata/ --since 30d --debug"
# parses fixtures, prints facts, makes zero network calls
```
**Done when:** you can unplug the network and this still works.

### Runs 9–12 — OpenAI, end to end
Verified in order with: `scan --dry-run` → `doctor` (verify) → `scan --since 1d --debug` (facts on stdout)
→ `scan --since 1d` + `doctor` (facts in db) → `scan --since 30d`, `^C`, re-run (resumes from cursor).
Each of these is its own run with its own verify block; I'll expand them when we get there so the
commands match what actually got built.

### Runs 13–22 — the report
Every run here is verifiable offline against fixtures: `make dev ARGS="usage --fixture testdata/"` plus the
view flag that run added. No API key needed to check my work.

### Runs 23–26 — trust and ship
`connect` / `disconnect` / `purge` need a real keychain, so run 23 is the one where you'll get an OS
password prompt. `purge` is verified by `ls ~/.aispend` returning nothing and the keychain entry being gone.

---

## Standing verification (run this any time)

```bash
go build ./... && go vet ./... && go test ./...
grep -rn "telemetry\|analytics.io\|posthog\|segment" --include="*.go" .   # must be empty
sqlite3 ~/.aispend/aispend.db "select * from connection;"                 # never a secret
```

## Definition of done
Unchanged from §12 of the design. The build is finished when a stranger goes from download to a number in
under 60 seconds, the total reconciles to your invoices within 2%, `purge` leaves no trace, and someone who
is not you has run it on a machine you did not set up.
