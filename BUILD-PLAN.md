# `aispend` — Incremental Build Plan (run-by-run)

**Companion to:** `../aispend-cli-design.md` (the design is the spec; this file is the execution order)
**Project root:** `ai-gov/aispend/`
**Module path:** `github.com/prabhuvmk/aispend` — placeholder, one-pass rewrite if you want a different one.
**Binary name:** `aispend` — also a placeholder per the design's opening note. Hardest thing to change later.

**The contract per run:** I implement one slice → you run the **Do this** block → you check it against
**Success criteria** → you tell me which numbered criterion failed, or "go" → next run.

**Runs 1 and 2 are implemented and frozen.** Anything this plan gains from a later re-read of the design
lands in **run 3 or later**, never as a retro-edit to a run you've already verified. If something in runs
1–2 turns out to be genuinely wrong, it becomes a named fix inside the next run rather than a silent
rewrite of history.

---

## Progress

- [x] **Run 1** — walking skeleton
- [x] **Run 2** — config paths + `doctor`
- [x] **Run 3** — catalog + `connections` + colour/TTY/UTF-8 layer + `config.toml`
- [x] **Run 4** — SQLite + schema v1 + migrations + `fact_id` + `doctor` db health
- [x] **Run 5** — `Sink` interface + `SQLiteSink` + fact envelope + `debug seed`
- [x] **Run 6** — credential resolver (env), masked display, `doctor` credentials block
- [x] **Run 7** — egress guard in the dialer + `scan --dry-run`
- [x] **Run 8** — fixture mode (`--fixture`, persistent flag)
- [x] **Run 9** — `Collector` interface + `Verify` + `doctor` vendors block
      *(live-network criterion deferred: no vendor admin key available on an individual plan)*
- [x] **Run 10** — OpenAI `Collect()`, facts to stdout, `--keep-raw`, `fmtutil` started
- [x] **Run 11** — persist through the sink, `sync_state`, restatement revisions
- [x] **Run 12** — pagination + resume, collector runtime (retry, rate limit, isolation), `sync`
- [x] **Run 13** — `fmtutil`, `usage`, the headline number, and the generated footer
- [x] **Run 14** — `BY VENDOR` table, grouping queries, `--vendor`, `--detail`
- [x] **Run 15** — Anthropic collector, schema v2 for cache-write tokens
- [x] **Run 16** — effective-dated price book, `amount_basis`, Basis footer
- [x] **Run 17** — `BY MODEL` and every `--by` dimension
- [ ] **Run 18** — sparklines, deltas, ASCII fallback  ← next
- [ ] Runs 19–26 below

---

## Ground rules carried through every run

- `make dev ARGS="..."` rebuilds and runs. `make check` = build + vet + test.
- **A run is not done until `make check` is clean.** No exceptions, no "I'll fix the test next run".
- Global `--debug` writes to **stderr only**, so `cmd --debug 2>/dev/null` always yields a clean report.
- No dependency outside the design's §2.4 list: cobra, modernc.org/sqlite, go-keyring, go-pretty, x/term.
  If a run seems to need a sixth, that's a design conversation, not a `go get`.
- Money is `int64` micros. Never a float, in any run, in any intermediate calculation.
- Dates are UTC `YYYY-MM-DD` text. Converted to local only at display time, never before.
- **Unknown ≠ zero.** Any run that renders an unavailable figure as `0` has introduced a bug, even if
  the tests pass.
- Every run ends with **Do this** and **Success criteria**. Criteria are numbered so you can report
  "3 failed" instead of describing it.
- Tests always set `AISPEND_HOME` to a temp dir. Nothing in this build ever writes to your real
  `~/.aispend` except commands you type yourself.
- **zsh note:** to inspect stderr alone, use `cmd 2>&1 1>/dev/null` **without a pipe**. zsh's MULTIOS
  duplicates stdout into the pipe as well, which makes it look like debug output is going to the wrong
  stream when it isn't.
- **Every temp state directory in a Do-this block starts with `rm -rf`, not `mkdir -p`.** These blocks
  get re-run, and the tail of one run leaves state that silently invalidates the head of the next — a
  criterion that "fails" because the previous pass left a config file behind wastes more time than the
  cleanup costs.
- **No standalone `#` comment lines in a Do-this block.** Interactive zsh does not enable
  `interactive_comments` by default, so a pasted `# Run 4` header runs as a command and prints
  `zsh: command not found: #`. Trailing comments after a real command are fine; section headers go in
  the prose around the block, or as `echo`.

---

## Run map

| Run | Slice | Design steps | Est. | Needs a key? |
|---|---|---|---|---|
| 1 | Skeleton: module, Makefile, cobra root, `version`, `--debug` | 1 | 30m | no |
| 2 | Config dir + `doctor` (paths block) | 2 | 30m | no |
| 3 | Embedded catalog + `connections` | 3 | 45m | no |
| 4 | SQLite open + migrations + `doctor` db health | 4 | 1h | no |
| 5 | `Sink` interface + `SQLiteSink` + `debug seed` | 5 | 45m | no |
| 6 | Credential resolver (env only) | 6 | 45m | no |
| 7 | Egress-guarded HTTP client + `scan --dry-run` | 7 | 1h | no |
| 8 | Fixture mode | 15 | 45m | no |
| 9 | OpenAI `Verify()` — first real network call | 8 | 1h | **yes** |
| 10 | OpenAI `Collect()`, one day, stdout only | 9 | 2h | **yes** |
| 11 | Persist through the sink | 10 | 45m | **yes** |
| 12 | Backfill, cursors, resume | 11 | 1.5h | **yes** |
| 13 | `fmtutil` + `usage` total-only | 12, 13 | 1.5h | no |
| 14 | `BY VENDOR` table | 14 | 1h | no |
| 15 | Anthropic collector | 16 | 2h | **yes** |
| 16 | Price book, `amount_basis`, Basis footer | 17 | 1.5h | no |
| 17 | `BY MODEL` table | 18 | 1h | no |
| 18 | Sparklines + deltas + ASCII fallback | 19 | 1.5h | no |
| 19 | OpenRouter collector | 20 | 1.5h | **yes** |
| 20 | `owners.csv` + `--by team` + Unattributed | 21 | 2h | no |
| 21 | Surprise rules + `⚠` block | 22 | 2h | no |
| 22 | `--json` / `--csv` / `export` | 23 | 1h | no |
| 23 | `connect` + `disconnect` + `purge` | 24, 25 | 2.5h | keychain |
| 24 | `export --share` | 26 | 45m | no |
| 25 | Error-message pass + redacting writer | 27 | 1h | no |
| 26 | README + release build | 28, 29 | 4h | no |

**Deviation from the design doc, and why:** fixture mode moves from step 15 to run 8, before the first
collector. The design already calls it "worth every minute"; putting it *before* the OpenAI collector
means every collector is written against a deterministic fixture first and the live call is just a swap
of the transport. It also means runs 13–22 are verifiable by you with **no API key and no spend**.

**If we fall behind, cut in this order:** run 19 (OpenRouter) → run 21 (surprises) → the `--csv` half of
run 22. **Never cut runs 7, 8, 20, 25** — the egress guard is the security claim, fixture mode pays for
itself within a day, the attribution block *is* the pitch, and run 25 is the difference between a binary
a stranger runs and one they abandon.

---

## Design coverage map — every section of the design, and where it lands

The check to re-run whenever either document changes. If a design section has no run number, it isn't
getting built.

| Design § | Requirement | Run |
|---|---|---|
| §1 | Credentials never in SQLite; env or keychain only | 6, 23 |
| §1 | Shortest path to the first number involves no configuration | done-criterion 1 |
| §2.2 | `modernc.org/sqlite`, cgo-free | 4, 26 (cross-compile proves it) |
| §2.3 | Catalog compiled in via `go:embed`, not in the DB | 3 |
| §2.3 | Price book effective-dated | 16 |
| §2.4 | Dependency budget defended | ground rules + deviation 2 |
| §3 | Catalog is the single source of truth for endpoints **and** allowed hosts | 3, 7 |
| §3 | Sink as an interface, not a direct write | 5 |
| §3 | Guards cross-cutting: egress in the dialer, redacting writer on stdout/stderr | 7, 25 |
| §4 | `scan` · `connect` · `connections` · `disconnect` · `sync` · `usage` · `export` · `doctor` · `purge` | 7 · 23 · 3 · 23 · 12 · 13 · 22 · 2+9 · 23 |
| §5 | Schema v1: `connection`, `usage_fact`, `sync_state`, `meta` | 4 |
| §5 | `amount_micros` integer, `amount_basis`, `cached_units` separate, `revision`, UTC text `day` | 4, 11, 15, 16 |
| §5 | Directory `0700`, files `0600`, `raw/` only with `--keep-raw` | 2 ✅, 10 |
| §6.1 | Progress lines · total · BY VENDOR · BY MODEL · ATTRIBUTION · `⚠` · `Next` | 15 · 13 · 14 · 17 · 20 · 21 · 21 |
| §6.1 | Footer: `Basis` · `Privacy` · `Days` | 16 · 13 · 13 |
| §6.2 | Formatting rules, incl. **unknown renders `—`, never `0`** | 13 |
| §6.2 | Always descending by cost; long lists capped with a reconciling remainder | 14, 17 |
| §6.3 | `--by model\|team\|key\|project\|day`, `--vendor`, `--since`, `--detail`, `--json`, `--csv` | 17, 20, 14, 13, 22 |
| §6.3 | `NO_COLOR`, TTY detection, non-UTF-8 fallback | 3 ✅, 18 |
| §6.4 | `export --share` + the `surprised?` prompt | 24 |
| §6.5 | `owners.csv`, loud **Unattributed** | 20 |
| §6.6 | Error shape: what happened / what it means / what to do | 25 |
| §7.1 | `Collector` interface, streaming `emit`, transaction per batch | 9, 11 |
| §7.2 | Price book consulted only when the vendor doesn't report cost; unknown model warns | 16 |
| §7.3 | `errgroup` `SetLimit(4)`, token bucket, retry 429/5xx only, honour `Retry-After` | 12 |
| §7.4 | Idempotent re-collection, new `revision` on restatement, trailing 7-day re-pull | 11, 12 |
| §8 | No telemetry, greppable | standing check |
| §8 | Egress allowlist enforced in the dialer; `--dry-run` prints the hosts | 7 |
| §8 | Credentials truncated in display, absent from DB / config / logs / JSON | 6, 22, 23 |
| §8 | Redacting writer catches a credential even in a panic | 25 |
| §8 | Least privilege documented honestly; signed releases; MIT or Apache-2.0 | 26 |
| §9.2 #1 | Sink interface | 5 |
| §9.2 #2 | Versioned fact envelope | 5 |
| §9.2 #3 | Deterministic `fact_id` across machines | 4, 5 |
| §9.2 #4 | `config.toml` read even though v1 doesn't need it | 3 |
| §9.4 | The footer must be **generated** from sink config — "make that a test, not a convention" | 13 |
| §10 | Scope boundary | *Not in v1*, below |
| §11 | `make dev`, `--debug` on day one, a commit per step | 1 ✅, ground rules |
| §12 | Definition of done | bottom of this file |

## What's alive when

The design's §11 table, remapped to these run numbers.

| After run | Catalog | Store | Creds | Collectors | Pricing | Analytics | Renderer | Guards |
|---|:--:|:--:|:--:|:--:|:--:|:--:|:--:|:--:|
| 5 | ✓ | ✓ | – | – | – | – | partial | – |
| 8 | ✓ | ✓ | ✓ | – | – | – | partial | ✓ |
| 12 | ✓ | ✓ | ✓ | 1 | – | – | partial | ✓ |
| 15 | ✓ | ✓ | ✓ | 2 | – | ✓ | ✓ | ✓ |
| 19 | ✓ | ✓ | ✓ | 3 | ✓ | ✓ | ✓ | ✓ |
| 22 | ✓ | ✓ | ✓ | 3 | ✓ | ✓ | ✓ | ✓ |

---

## Not in v1 — the boundary that makes this a one-week build

Straight from design §10. If a run starts drifting toward any of these, the run is wrong, not the list:

❌ Cloud connectors (Bedrock, Vertex, Azure OpenAI) · ❌ card feeds, expense imports, SSO log ingestion ·
❌ any server, account, or sync-to-cloud · ❌ scheduled or daemon sync · ❌ budgets, alerts, anomaly
*notification* (the `⚠` block is computed at report time only) · ❌ the tagging SDK · ❌ Windows polish
beyond "it compiles and runs" · ❌ more than three vendors · ❌ any vendor without a first-class usage API
(Copilot, Cursor, ElevenLabs, Perplexity).

**The trade-off being accepted, named:** three vendors measures *API spend only* — the bucket where the
LLM-observability tools already live. The seat bucket (Copilot, Cursor, ChatGPT Business) is where the
differentiation actually is, because no competitor spans both. So **Copilot is the first connector after
validation**, not a someday item, and when a sharp prospect asks "what about our Copilot seats?", that's
a buying signal, not a gap. The schema already carries `unit_kind` and `other_units` unused, so adding a
seat-denominated vendor later is a new file, not a migration.

## Deviations from the design, and why

Three, all deliberate. Everything else follows the design as written.

1. **Fixture mode moves from step 15 to run 8** — before the first collector, so every collector is
   written against deterministic fixtures and the live call is a transport swap. Also makes runs 13–22
   verifiable with no API key and no spend.
2. **`text/tabwriter` instead of `github.com/jedib0t/go-pretty/v6`.** Run 2's table needed alignment and
   the stdlib did it in four lines. That takes the dependency count from 5 to **4**, and §2.4's whole
   argument is that a `go.mod` a security reviewer opens is part of the UX. If a later table needs
   something tabwriter can't do, adding go-pretty then costs nothing. **Say the word if you'd rather
   have it from the start.**
3. **Anthropic's `Verify` pulled forward from run 15 into run 9** (deviation added after the fact).
   Run 9's point is the first real network call, and a run you cannot verify is not delivered. The
   Anthropic Admin API endpoints were confirmed against current documentation, the `Verify` shape is
   identical to OpenAI's, and it is ~60 lines. Anthropic's `Collect` stays in run 15 where it belongs.
4. **A hand-rolled TOML subset instead of a TOML library** (run 3). The config schema is five keys in
   two sections; a parser for exactly that is ~110 lines with better error messages than a general
   library gives, and it keeps direct dependencies at 4. Comments, sections, quoted strings, integers
   and booleans are supported; anything else is a clear error naming the file and line, so a user hits a
   wall rather than a silent misparse. If the schema outgrows it, that's the moment to reconsider.
5. **The §9.2 "do it now" items are pinned to specific runs** rather than left as a section: config file
   → run 3, `fact_id` → runs 4 and 5, fact envelope → run 5, `Sink` interface → run 5. All four are
   cheap today and expensive to retrofit, which means they need a run number or they don't happen.

---

# Run detail

---

## Run 1 — walking skeleton ✅

**Build:** `go.mod`, `Makefile`, cobra root with `--debug` / `--no-color`, `version` from `-ldflags`.
**Files:** `main.go`, `Makefile`, `internal/cli/{root,version}.go`, `internal/buildinfo/`, `internal/dbg/`

**Do this**
```bash
make check
make dev ARGS=""
make dev ARGS="version"
make dev ARGS="version --debug"
make dev ARGS="version --debug" 2>/dev/null
```

**Success criteria**
1. `make check` — compiles, `go vet` silent, tests pass.
2. Bare invocation prints help listing `version` and the `--debug` flag.
3. `version` prints `aispend <ver> (<sha>, go1.26.0, darwin/arm64)`.
4. `--debug` adds `debug ` lines; with `2>/dev/null` they vanish and stdout is unchanged.


**Outcome.** The repository exists and produces a binary that runs. `make dev ARGS="..."` rebuilds and
executes in one keystroke, `version` reports the release, commit, toolchain and target from link-time
variables, and a global `--debug` writes to stderr through a dedicated channel so internal detail can
never contaminate a report that is being piped or redirected. Nothing here is a feature a user would
name, and that is the point: every later run inherits a build loop, an identity and a debug channel that
already work, rather than adding them under pressure on the day something breaks.

---

## Run 2 — config paths and `doctor` ✅

**Build:** `~/.aispend/` at `0700`, `AISPEND_HOME` override, permission repair, `doctor` paths block.
**Files:** `internal/config/config.go`, `internal/cli/doctor.go`, `internal/cli/symbols.go` + tests

**Do this**
```bash
make check
rm -rf ~/.aispend && ./aispend doctor
ls -ld ~/.aispend
rm -rf /tmp/as1 && AISPEND_HOME=/tmp/as1 ./aispend doctor --debug
chmod 755 /tmp/as1 && AISPEND_HOME=/tmp/as1 ./aispend doctor >/dev/null && ls -ld /tmp/as1
```

**Success criteria**
1. `make check` clean; `ok` for `internal/cli` and `internal/config`.
2. Fresh `doctor` prints three aligned rows: config `(0700 ✓)`, db `(missing)`, owners `(missing · optional)`.
3. `ls -ld ~/.aispend` → `drwx------`.
4. With `AISPEND_HOME` set, every printed path is under the override and real `~/.aispend` is untouched.
5. Debug lines appear on stderr only.
6. A loosened directory is back to `drwx------` after the next `doctor`.

### `doctor` is assembled across six runs — don't judge it until run 19

`doctor` is a **network** command in the finished tool (design §4): the one you tell a hesitant prospect
to run first, because it makes one cheap read call per configured vendor and reports what it could see
without collecting anything. It is low commitment, and it surfaces credential problems before they turn
into a confusing empty report.

It arrives one block at a time, because each block depends on machinery that doesn't exist yet:

| Block | Lands in | Depends on |
|---|---|---|
| `paths` | run 2 ✅ | nothing |
| `db` | run 4 | SQLite + migrations |
| `credentials` | run 6 | the credential resolver |
| `vendors` — reachability and what each key could read | run 9 (openai), 15 (anthropic), 19 (openrouter) | the egress-guarded client + each collector's `Verify()` |

Target shape once all six runs have landed:

```
  paths        config  ~/.aispend/            (0700 ✓)
               db      ~/.aispend/aispend.db  (0600 ✓)
               owners  ~/.aispend/owners.csv  (missing · optional)

  db           ok · schema v1 · 4,182 facts · 2 connections
               covered  2026-07-29 → 2026-08-27

  credentials  openai      env OPENAI_ADMIN_KEY   (sk-…a4f2)
               anthropic   keychain               (sk-…9c01)
               openrouter  none

  vendors      openai      ✓ reachable · org acme-corp · 3 projects · 28 keys   1.2s
               anthropic   ✗ 403 · key is not an Admin key — see: aispend connect anthropic
               openrouter  — no credential configured
```

**Two distinctions that block must keep** (both are the §6.2 "unknown ≠ zero" rule applied to
diagnostics, and both are worth stating now so run 9 doesn't get them wrong):

- **No credential** is not **unreachable**. A vendor with no key prints `—`, never a failure.
- **Unreachable** is not **rejected**. A prospect behind a corporate egress proxy will hit a dial
  failure, and telling them "your key is bad" sends them to regenerate a perfectly good admin key.
  Run 9 must separate "couldn't open the connection" from "the vendor answered and said no."

Until run 9, `doctor` printing only paths is correct-by-design, not an omission — there is nothing to
reach out to yet, and the egress guard that makes reaching out safe doesn't exist until run 7.


**Outcome.** aispend now owns a place on disk. `~/.aispend/` is created at `0700` on demand and
actively repaired if it is found looser, `AISPEND_HOME` redirects the whole state directory so tests and
experiments never touch your real one, and `doctor` prints its first block: where each file lives and
whether it is in the state the tool expects. Two distinctions that carry through the rest of the build
were settled here — a missing file is a normal result rather than an error, and "absent" prints
differently from "present but wrong", because they need different reactions from the reader. Having one
directory that holds everything is also what makes `purge` a thirty-line command later instead of a hunt.

---

## Run 3 — catalog and the first table ✅

**Build:** `catalog.json` embedded with `//go:embed` — three vendors, each with display name, unit kind,
env var names, API endpoints, **allowed hosts**, and a conservative rate limit. `connections` (alias `ls`)
renders it. Colour/TTY/UTF-8 capability decided once, here, so every later view inherits it.

**Plus `~/.aispend/config.toml`** with the near-empty schema from design §9.2 #4, read but barely used.
A daemon can't be configured by flags and env alone, and adding a config file later means changing every
invocation path — so the precedence chain (flag > env > file > default) gets established now, while there
is exactly one caller.

**Why the allowed hosts live in the catalog and not a separate list:** the security guarantee and the
vendor definition then cannot drift apart, because they are the same file (design §3).

**Do this**
```bash
make check
./aispend connections
./aispend ls | head -2
./aispend connections | cat -v | grep '\^\[' || echo "no ANSI — good"
NO_COLOR=1 ./aispend connections | tail -1
LC_ALL=C ./aispend connections | tail -1
LC_ALL=C ./aispend doctor
./aispend connections --debug 2>&1 1>/dev/null | grep allows

rm -rf /tmp/as-cfg && mkdir -p /tmp/as-cfg     # clean — a leftover config.toml invalidates the next line
AISPEND_HOME=/tmp/as-cfg ./aispend connections --debug 2>&1 1>/dev/null | grep "no config"
printf 'debug = yes\n'   > /tmp/as-cfg/config.toml && AISPEND_HOME=/tmp/as-cfg ./aispend connections
printf 'colour = true\n' > /tmp/as-cfg/config.toml && AISPEND_HOME=/tmp/as-cfg ./aispend connections
printf 'debug = true\n'  > /tmp/as-cfg/config.toml
AISPEND_HOME=/tmp/as-cfg ./aispend version 2>&1 1>/dev/null                  # debug lines
AISPEND_HOME=/tmp/as-cfg ./aispend version --debug=false 2>&1 1>/dev/null    # silent
AISPEND_HOME=/tmp/as-cfg AISPEND_DEBUG=0 ./aispend version 2>&1 1>/dev/null  # silent

rm -rf /tmp/as-r2 && AISPEND_HOME=/tmp/as-r2 ./aispend doctor && ls -ld /tmp/as-r2
```

**Success criteria**
1. `make check` clean.
2. A table with a header and one row per vendor: `openai`, `anthropic`, `openrouter` — each showing unit
   `token` and status `not connected`.
3. `ls` is an exact alias of `connections`.
4. Piping produces **zero ANSI escape sequences** (`cat -v` shows no `^[`).
5. `NO_COLOR=1` on a TTY produces the same plain text.
6. `LC_ALL=C` falls back to ASCII — no `✓`, no box-drawing characters, still aligned.
7. A vendor's allowed hosts are visible somewhere — `--debug` printing them is enough at this stage.
8. A missing `config.toml` is **not** an error — the tool runs on defaults and says nothing about it.
9. A malformed `config.toml` names the file and line, and does not panic.
10. Precedence holds: `debug = true` in the file produces debug output; `--debug=false` and
    `AISPEND_DEBUG=0` each silence it. Flag beats env beats file.
11. **Run 2 regression:** `doctor` on a fresh `AISPEND_HOME` still prints `(0700 ✓)` and creates
    `drwx------`. The status glyphs moved into `internal/ui` in this run, so this confirms nothing you
    already verified broke.


**Outcome.** This is the first run whose output looks like a product. `connections` renders the three
supported vendors from a catalog compiled into the binary, so a fresh install has a working vendor list
with no bootstrap step and no migration when a vendor is added. That catalog also carries each vendor's
permitted hosts, which is what makes the run 7 egress guard a mechanism rather than a policy: the
security guarantee and the vendor definition are the same file and cannot drift apart. Alongside it, the
`ui` package settles colour, TTY detection and UTF-8 capability once — `NO_COLOR`, `TERM=dumb`, piped
output and non-UTF-8 locales all resolve in one place, with ASCII fallbacks for every glyph — so no later
view has to re-decide it and get it subtly different. Finally `config.toml` lands with a near-empty
schema, establishing the flag > env > file > default precedence chain while there is exactly one caller,
rather than after a dozen flags have grown their own habits.

---

## Run 4 — SQLite and migrations ✅

**Build:** `modernc.org/sqlite` (pure Go, no cgo), schema v1 exactly as design §5 — `connection`,
`usage_fact`, `sync_state`, `meta` — a migration runner keyed on `meta.schema_version`, db file at `0600`,
`doctor` gains a database line.

**Two departures from the design's §5 listing, both forced by the same SQLite behaviour** — a PRIMARY
KEY column may be NULL, and NULL never equals NULL. `workspace_ref`, `principal_ref` and `model_ref` are
therefore `NOT NULL DEFAULT ''`: left nullable, two collections of the same fact with no principal would
not conflict and re-running a scan would double the row. Empty string means "the vendor does not report
this dimension", and the renderer shows it as an em dash, never a zero. The tables are also `STRICT`, so
`amount_micros` cannot quietly accept a float — the integer-money rule enforced by the schema instead of
by discipline.

**Plus one column the design's §5 listing doesn't show but §9.2 #3 requires:** `fact_id`, a deterministic
`sha256(vendor, day, workspace_ref, principal_ref, model_ref)`. The §5 primary key is *local*; a server
receiving facts from many agents needs a stable identity to dedupe against, and agents retry, resend and
overlap windows constantly. Adding the column now is one line. Adding it once agents are deployed in the
field and can't all be upgraded at once is not.

**Do this**
```bash
make check
rm -rf /tmp/as2
AISPEND_HOME=/tmp/as2 ./aispend doctor
AISPEND_HOME=/tmp/as2 ./aispend doctor --debug 2>&1 | grep -i migrat
ls -l /tmp/as2/aispend.db
sqlite3 /tmp/as2/aispend.db ".tables"
sqlite3 /tmp/as2/aispend.db "select * from meta;"
sqlite3 /tmp/as2/aispend.db ".schema usage_fact"
otool -L ./aispend | head -5     # macOS: proves no libsqlite3 linkage
```

**Success criteria**
1. `make check` clean.
2. `doctor` prints `db  ok · schema v1 · 0 facts · 0 connections`.
3. `.tables` lists exactly `connection  meta  sync_state  usage_fact`.
4. `meta` contains `schema_version=1` and an `install_id`.
5. `ls -l` on the db shows `-rw-------`.
6. **Second run migrates nothing** — the debug output says so, and the output is identical.
7. `.schema usage_fact` shows `amount_micros INTEGER` (not REAL) and the composite primary key ending
   in `revision`.
8. `fact_id` exists and is indexed.
9. `otool -L` shows no SQLite C library — the pure-Go driver is what's linked.


**Outcome.** State becomes durable. Schema v1 lands in SQLite through a migration runner keyed on
`meta.schema_version`, the database file is `0600`, and `doctor` gains a health line reporting schema
version, fact count and coverage. The driver is pure Go, which is what keeps `GOOS=linux go build` a
single command instead of a cross-compilation toolchain project — the payoff arrives in run 26 but the
decision has to be made here. The table shapes are deliberately a subset of the full product schema that
migrates forward cleanly, and `fact_id` is added now so facts have an identity that survives leaving the
machine.

---

## Run 5 — sink interface and a fake ✅

**Build:** the `Sink` interface from design §9.2, `SQLiteSink` writing a batch in one transaction, and a
hidden `debug seed` command emitting three synthetic facts. This is the whole pipeline shape with no
network in it — everything after this fills in real parts.

**Plus the versioned fact envelope** (§9.2 #2): every batch is wrapped as
`{"schema":1,"agent":"0.1.0","install_id":"…","sent_at":"…","facts":[…]}`. v1 never transmits it, so this
looks like dead weight — it isn't. The moment facts leave the machine, agent and server version
independently and *will* drift; a server that can reject unknown majors and tolerate unknown fields is
free to build today and impossible to retrofit across a fleet of deployed agents. `debug seed --envelope`
prints one so it's a real, exercised code path rather than a struct nobody has ever marshalled.

**Do this**
```bash
make check
rm -rf /tmp/as3
AISPEND_HOME=/tmp/as3 ./aispend debug seed
AISPEND_HOME=/tmp/as3 ./aispend doctor
AISPEND_HOME=/tmp/as3 ./aispend debug seed
AISPEND_HOME=/tmp/as3 ./aispend doctor
sqlite3 /tmp/as3/aispend.db "select vendor,day,model_ref,amount_micros,amount_basis from usage_fact;"
./aispend --help | grep -c debug || echo "hidden — good"
```

**Success criteria**
1. `make check` clean.
2. After the first seed, `doctor` reports `3 facts`.
3. After the **second** seed, still `3 facts` — the composite primary key deduped it. This is the
   idempotency the collectors depend on; if it's 6, stop and tell me.
4. `amount_micros` values are integers (e.g. `41200000`, not `41.2`).
5. `amount_basis` is populated on every row.
6. `debug` does not appear in the top-level help.
7. A killed seed mid-write leaves 0 rows, not a partial batch — the transaction holds.
8. `fact_id` is **deterministic** — the same fact seeded on two different machines produces the same id.
   A unit test with hardcoded inputs and a hardcoded expected hash, so a refactor can't silently change it.
9. `debug seed --envelope` emits valid JSON carrying `schema`, `agent`, `install_id`, `sent_at`.
10. Nothing in the envelope path opens a socket. Grep the run: `Sink` writes to SQLite and only SQLite.


**Outcome.** The pipeline is complete end to end with no network in it. Facts flow through a `Sink`
interface into SQLite in a single transaction per batch, `debug seed` produces synthetic facts so the
whole path can be exercised and re-exercised, and re-seeding proves the composite primary key dedupes
rather than double-counts — the property every collector will lean on. The sink being an interface is
twenty lines that save a day later: it is the seam the sidecar posture opens along when a second
destination appears. The versioned fact envelope ships here for the same reason, while it costs nothing
and there are no deployed agents to upgrade.

---

## Run 6 — credential resolver (environment only) ✅

**Build:** resolution order env → keychain (keychain stubbed this run), truncated display (`sk-…a4f2`),
and the structural guarantee that the resolver has no database handle at all. `connections` shows key
status per vendor, and `doctor` gains its credentials block.

**Redaction is a property of the type, not of its call sites.** The secret is an unexported field, and
`String`, `GoString` and `MarshalJSON` all mask — so a credential that reaches a `%v`, a `%#v`, a wrapped
error, a log line or a panic prints `sk-…a4f2`. Reading the real value takes an explicit `Secret()` call,
which is one greppable name to audit rather than every format verb in the codebase.

**Do this**
```bash
make check
./aispend connections
OPENAI_ADMIN_KEY=sk-test-0000000000000000a4f2 ./aispend connections
OPENAI_ADMIN_KEY=sk-test-0000000000000000a4f2 ./aispend connections --debug 2>&1 | grep -i sk-test || echo "not leaked — good"
AISPEND_HOME=/tmp/as3 OPENAI_ADMIN_KEY=sk-test-0000000000000000a4f2 ./aispend connections
grep -c "sk-test" /tmp/as3/aispend.db || echo "not in db — good"
```

**Success criteria**
1. `make check` clean.
2. With no env vars: all three vendors `not connected`.
3. With the key set: openai shows `key in env OPENAI_ADMIN_KEY (sk-…a4f2)`, the others unchanged.
4. **The full key never appears in any output, including `--debug`.** Only the truncated form.
5. `grep` finds no trace of the key in the database file.
6. Unsetting the env var returns openai to `not connected` — nothing was persisted.
7. An exported-but-empty variable (`export OPENAI_ADMIN_KEY=`) and a whitespace-only one are **not**
   credentials — that is the shape a half-finished shell profile leaves behind.
8. `doctor` gains a credentials block; a vendor with no key shows an em dash and `no credential`, never
   a failure.
9. A test asserts the `cred` package's whole dependency tree excludes `database/sql`, the store, the
   sink and `net/http`. The separation is then enforced by the build rather than by review.


**Outcome.** The design's central separation becomes structural rather than aspirational.
Credentials resolve from the environment, display only in truncated form, and the resolver has no
database handle at all — not a rule someone has to remember, but a type that cannot write a secret
because it was never given anywhere to write one. `connections` now distinguishes a configured vendor
from an unconfigured one, which is the first half of the question `doctor` will answer properly in run 9.

---

## Run 7 — the egress guard ✅

**Build:** `http.Transport` whose `DialContext` refuses any host absent from the catalog (design §8), plus
`scan --dry-run` printing the exact hosts and requests it *would* make, then exiting. And the test that a
non-catalog host is refused — **that test is the security claim**, so it is written as carefully as the code.

**Do this**
```bash
make check
go test ./internal/egress/... -v
./aispend scan --dry-run
./aispend scan --dry-run --since 7d
grep -rn "telemetry\|posthog\|segment\|amplitude" --include="*.go" . || echo "none — good"
```

**Success criteria**
1. `make check` clean.
2. `go test -v` shows a passing test named for blocking an unlisted host, and one confirming a catalog
   host is allowed.
3. `--dry-run` prints a line per planned request (`would GET api.openai.com/… ×30`) and **makes no
   network call** — verify by running it with wifi off; the output must be identical.
4. `--dry-run` exits 0 and writes nothing to the database.
5. Changing `--since` changes the printed request count.
6. The telemetry grep finds nothing. This grep is in the standing checks and should stay empty forever.


**Outcome.** The security claim stops being a sentence in a README and becomes a mechanism. The HTTP
dialer refuses any host absent from the catalog, so no code path — not a collector, not a future feature,
not a mistake — can reach an unlisted destination, and the test asserting it is written with the care of
production code because it *is* the claim. `scan --dry-run` prints the exact hosts and requests the
binary would make and exits, which is the command a hesitant prospect runs before they let it near a key,
and the input the report's `Privacy` footer is generated from.

---

## Run 8 — fixture mode ✅

**Build:** `--fixture <dir>` swaps the transport for one serving canned JSON from disk. Checked-in
fixtures for the OpenAI usage and organisation endpoints. From here the renderer iterates in a tight
loop with no API calls, no rate limits and no spend — and the fixtures double as deterministic tests.

**Do this**
```bash
make check
ls testdata/
./aispend scan --fixture testdata/ --since 30d --debug
echo "now turn wifi off and run the next line"
./aispend scan --fixture testdata/ --since 30d
```

**Success criteria**
1. `make check` clean.
2. Fixture files are readable JSON you can open and edit by hand.
3. `scan --fixture` produces parsed output with the network **off**.
4. Debug output shows fixture reads, not HTTP requests.
5. Two consecutive runs produce byte-identical output — determinism is the point.
6. A malformed fixture produces a clear error naming the file, not a panic.
7. `--fixture` is a **persistent** flag, so `doctor` can be driven by it too — that is the command
   you most want to run without a network.
8. A missing fixture lists what it looked for and what the directory actually contains.

**Scope correction, declared rather than glossed:** this run's original criterion 3 said fixture mode
would "print facts". It cannot — parsing a vendor response into facts is the collector's job, and the
first collector is run 9/10. What run 8 delivers is the transport: a full HTTP round trip served from
disk, exercised end to end by `doctor --fixture`. Facts appear in run 10 as planned.


**Outcome.** Development stops costing money and stops depending on the network. `--fixture` swaps
the transport for one serving canned JSON, so every collector after this is written against a
deterministic response first and the live call becomes a transport swap rather than a leap. The same
fixtures give the renderer a stable dataset to iterate against and the test suite deterministic inputs
for free. It is placed before the first collector rather than after three, which is the one deliberate
reordering of the design's build plan in this document.

---

## Run 9 — OpenAI `Verify()` · first real network call ✅

**Needs:** `OPENAI_ADMIN_KEY` (an **Admin** key — a normal API key won't read org usage).

**Build:** the `Collector` interface from design §7.1 and OpenAI's `Verify` — one cheap read call
reporting what the credential could actually see. `doctor` gains a per-vendor reachability line.

**Do this**
```bash
make check
export OPENAI_ADMIN_KEY=<your admin key>
./aispend doctor
OPENAI_ADMIN_KEY=sk-obviously-wrong ./aispend doctor
unset OPENAI_ADMIN_KEY && ./aispend doctor     # no credential — must print —, not an error
echo "now turn wifi off, set a good key again, and re-run ./aispend doctor"
```

**Success criteria**
1. `make check` clean.
2. `doctor` grows a `vendors` block and prints `openai  ✓ reachable · org <name> · N projects · M keys`.
3. A bad key produces the design §6.6 shape: what happened, what it means, what to do — and **no raw
   HTTP body, no stack trace**.
   3a. A non-network failure (a malformed fixture, a TLS error) must **not** be reported as a
   connectivity problem. `http.Client` wraps everything in `*url.Error`, which itself satisfies
   `net.Error`, so the obvious type check blames the firewall for everything. Unwrap first.
4. A normal (non-admin) key produces a message naming the admin-key requirement specifically.
5. **A vendor with no credential prints `—`, not a failure.** Absent is not broken.
6. **Unreachable is distinguished from rejected.** Block the host (`/etc/hosts` or wifi off) and the
   message must be about connectivity, not credentials — otherwise a prospect behind a proxy regenerates
   a perfectly good admin key and blames you for the wasted hour.
7. Timing is shown per vendor and is under a couple of seconds.
8. Nothing was written to the database — `Verify` reads only.


**Outcome.** The binary talks to a vendor for the first time, and `doctor` becomes the network
diagnostic the design intends: one cheap read per configured vendor, reporting what the credential could
actually see. Three states are kept distinct — no credential, unreachable, and rejected — because
collapsing them is how a prospect behind a corporate proxy is told their perfectly good admin key is bad
and concludes the tool is broken. Nothing is written to the database; `Verify` only reads.

---

## Run 10 — OpenAI `Collect()`, one day, stdout only ✅

**Build:** `Collect` streaming facts through an `emit` callback, mapping OpenAI's response onto the
`usage_fact` shape. Deliberately **not persisted yet**, so you can eyeball the mapping against the raw
response before it's in a table.

**Do this**
```bash
make check
./aispend scan --since 1d --debug
./aispend scan --since 1d --debug --keep-raw && ls ~/.aispend/raw/
AISPEND_HOME=/tmp/as4 ./aispend scan --since 1d && sqlite3 /tmp/as4/aispend.db "select count(*) from usage_fact;"
```

**Success criteria**
1. `make check` clean.
2. One `fact` line per row: `fact  openai 2026-08-27 proj_a91f key_9f2a gpt-5.2  in=1.2M out=84.2K
   cached=311K  — unknown`.
3. *(Deferred — needs a vendor key.)* Spot-check two facts against the OpenAI dashboard for the same
   day: model and tokens agree.

**Scope correction, declared.** Criterion 2 originally ended `$41.20 vendor_reported`. It cannot: OpenAI's
usage and cost endpoints are separate, and the cost endpoint cannot break spend down to model level.
Joining them *is* allocation, which the design itself places at step 17 (run 16). So every fact from this
run carries `amount_basis = 'unknown'` with a zero amount and renders as an em dash — which is the
design's own rule for an unpriced fact, not a shortcut. `$0.00` here would silently understate the total.
4. `cached_units` is populated separately from `input_units` where OpenAI reports it.
5. Fact count is 0 for a day with no usage, and the tool says "no usage in this range" rather than
   printing an empty report.
6. Database row count is still **0** — this run doesn't persist.
7. `--keep-raw` writes to `~/.aispend/raw/` at `0700` with `0600` files, and that directory does not
   exist without the flag. Failing to save a copy must never fail the collection.
8. A vendor bucket dated outside the requested window is **dropped**, with a debug line saying so. The
   window is the contract, and coverage tracking in run 12 trusts that stored days are collected days.
9. Token humanising rounds in integer arithmetic: `Tokens(1450)` is `1.5K`. Dividing in float first
   yields 1.4499999999999999556, which prints `1.4K`.


**Outcome.** Real usage data reaches the terminal. `Collect` streams facts through an `emit`
callback and prints them rather than persisting them, which exists purely so the mapping from a vendor's
response onto the fact schema can be checked against the vendor's own dashboard before anything is
stored. Cached tokens are separated from input tokens here, at the point of parsing, because folding them
together produces an error that grows as a customer optimises — precisely when they are checking your
work.

---

## Run 11 — persist through the sink ✅

**Build:** wire `Collect` → `SQLiteSink`, one transaction per emitted batch, `sync_state` updated on success.

**Do this**
```bash
make check
rm -rf /tmp/as5
AISPEND_HOME=/tmp/as5 ./aispend scan --since 1d
AISPEND_HOME=/tmp/as5 ./aispend doctor
AISPEND_HOME=/tmp/as5 ./aispend scan --since 1d
AISPEND_HOME=/tmp/as5 ./aispend doctor
sqlite3 /tmp/as5/aispend.db "select * from sync_state;"
sqlite3 /tmp/as5/aispend.db "select count(*), sum(amount_micros) from usage_fact;"
```

**Success criteria**
1. `make check` clean.
2. `doctor` reports a non-zero fact count and `covered 2026-08-27 → 2026-08-27`.
3. **Re-running does not change the count or the sum** — idempotent re-collection.
4. `sync_state` has one openai row with `last_run_at` set and `last_error` empty.
5. `sum(amount_micros)` matches the total from run 10's stdout.
6. Interrupting mid-scan leaves the db queryable and `doctor` still works.


**Outcome.** Facts become durable. The collector writes through the sink in a transaction per batch
and `sync_state` records how far it got, so a scan that is interrupted keeps what it collected. Re-running
the same window changes neither the row count nor the total, which is the idempotency the trailing
re-pull in the next run depends on.

---

## Run 12 — backfill, cursors, resume ✅

**Build:** 30-day backfill, cursor persisted after **every page**, resume from `sync_state`, trailing
7-day re-pull on subsequent syncs to catch vendor restatements (design §7.4). `sync` ships here as its own
command — refresh the cache, print no report.

**Interface change, declared:** `Collect` now takes an `Emitter` (`Emit` + `PageDone`) rather than a bare
`func(Fact) error`. The unit a collector produces (a fact) and the unit it can safely resume from (a page)
are different, and persisting the cursor after every page is what turns Ctrl-C during a backfill from
"start again" into "lose at most one page". Design §7.1's signature had nowhere to report a page boundary.
Two implementations, changed while the abstraction is still cheap to change — which is what the design
says run 15 is for.

**No new dependencies:** design §7.3 names `errgroup`, which is not on the §2.4 list. `errgroup` also
cancels its siblings on the first error, which is the *opposite* of what is wanted here — one vendor
failing must never abort the others. A `sync.WaitGroup` with a semaphore is smaller and semantically
correct, so direct dependencies stay at 3.

**Plus the collector runtime from design §7.3**, which has no other natural home: `errgroup` with
`SetLimit(4)` fanning out across vendors and staying sequential within one, a per-vendor token bucket
defaulting to 2 req/s from the catalog, and retry on **429 and 5xx only** — exponential backoff, jitter,
5 attempts, honouring `Retry-After`. Never retry any other 4xx: it's a credential or permission problem,
and retrying just delays a clear error behind thirty seconds of spinner.

**Do this**
```bash
make check
rm -rf /tmp/as6
AISPEND_HOME=/tmp/as6 ./aispend scan --since 30d      # ^C partway through
AISPEND_HOME=/tmp/as6 ./aispend doctor
AISPEND_HOME=/tmp/as6 ./aispend scan --since 30d      # let it finish
AISPEND_HOME=/tmp/as6 ./aispend sync
sqlite3 /tmp/as6/aispend.db "select day, count(*) from usage_fact group by day order by day;"
sqlite3 /tmp/as6/aispend.db "select max(revision) from usage_fact;"
```

**Success criteria**
1. `make check` clean.
2. After `^C`, `doctor` shows a partial range — the pages already fetched are **kept**.
3. The re-run prints `resuming openai from 2026-08-14 …` and doesn't refetch what it had.
4. Final row-per-day listing covers 30 consecutive days with no gaps in the middle.
5. `sync` re-pulls only the trailing window, prints **no report**, and the total is unchanged.
6. Restated days produce a **new revision**, not an overwrite — `max(revision)` may be 2, never a
   silently changed row.
7. Rate limiting is observable: `--debug` timestamps show requests spaced by the catalog's limit, not
   fired in a burst.
8. A 429 with `Retry-After: 2` is honoured, and an absurd value is capped rather than obeyed.
9. **A 403 is not retried.** Same test shape: one request, immediate clear error. If a bad key takes
   30 seconds to report itself, the retry policy is wrong.
10. `Ctrl-C` mid-backfill loses **at most one page**, not the run.


**Outcome.** The write path is finished. A thirty-day backfill resumes from a persisted cursor after
an interrupt, losing at most one page, and `sync` refreshes the cache without printing a report. The
collector runtime around it — bounded concurrency across vendors, a per-vendor token bucket, retry on 429
and 5xx only with `Retry-After` honoured — arrives in the same run, including the rule that no other 4xx
is ever retried, so a bad credential fails in one request instead of hiding behind thirty seconds of
spinner. Restated days append a new revision rather than overwriting, which turns "our number changed"
into "the vendor restated on the 14th".

---

## Run 13 — `fmtutil` and the first number ✅

**Build:** every rule from design §6.2 in one package with table tests, then `usage` printing the total
and nothing else. This is the moment it feels like a product — and formatting comes first so every later
view inherits it rather than reinventing it.

**Plus the report footer**, which is not decoration. The `Privacy` line is **generated from the egress
allowlist the binary actually enforced** — it names the hosts contacted, so it says something true and
verifiable rather than something marketed. The `Days` line states that all dates are UTC and how many
days at the range edges are incomplete. Design §9.4 makes this a hard rule: if a sink other than SQLite
is ever configured, the footer must say so instead of claiming nothing left the machine — **"make that a
test, not a convention."** That test is written in this run, while there is only one sink and it is
trivially true, because the run where it stops being trivially true is the run where you'd forget.

**Do this**
```bash
make check
go test ./internal/fmtutil/ -v
./aispend usage --fixture testdata/
./aispend usage --fixture testdata/ --since 7d
LC_ALL=C ./aispend usage --fixture testdata/
```

**Success criteria**
1. `make check` clean; `fmtutil` table tests cover each row of §6.2.
2. Output is the two-line header plus `Total  $5,881`.
3. Money ≥ $1,000 has **no decimals**; under $1,000 has exactly two (`$847.20`).
4. Tokens humanise to 3 significant figures (`421M`, `1.4B`).
5. An unavailable figure renders as `—` with a footnote — **never `0`**, never an omitted row.
6. Changing `--since` changes the total and the date range in the header.
7. Footer prints `Privacy  Nothing left this machine. …` naming **only** hosts actually contacted this
   run. An offline `usage`, or any run against fixtures, says `No network was used` — a claim that can be
   checked by unplugging the machine, which is the difference between evidence and marketing. A host the
   guard *blocked* is not a contact.
8. Footer prints `Days  All dates UTC. N days incomplete at the range edges.`
9. A test asserts the footer is **derived from the sink configuration**, not a constant: point the
   renderer at a two-sink config and the "nothing left this machine" wording must change on its own.
   Get this wrong once and you lose the only thing that makes a one-person company credible enough to
   be handed an admin key.


**Outcome.** The tool prints a number, and the moment it does it starts to feel like a product.
Every formatting rule lives in one package with table tests before any view exists, so no view can invent
its own money format, and the rule that an unavailable figure renders as an em dash rather than a zero is
enforced centrally. The report footer arrives with it: the `Privacy` line is generated from the egress
allowlist the binary actually enforced and the `Days` line states the UTC convention and any incomplete
edges. The test that the footer is derived from the sink configuration rather than hand-written is
written now, while it is trivially true, because the run where it stops being trivially true is the run
where it would be forgotten.

---

## Run 14 — `BY VENDOR` ✅

**Build:** the vendor table — descending by cost always, share percentages paired with absolutes, a
totals row that visibly reconciles, remainder line for long lists.

**Do this**
```bash
make check
./aispend usage --fixture testdata/
./aispend usage --fixture testdata/ --vendor anthropic
./aispend usage --fixture testdata/ | cat
```

**Success criteria**
1. `make check` clean.
2. Rows sorted **descending by spend**, every time.
3. Share column sums to 100% (±1 from rounding) and the totals row equals the reported total.
4. `--vendor` filters to one vendor and the total changes accordingly.
5. Piped output has no ANSI codes and stays column-aligned.
6. A vendor with no data shows `—`, not `$0`.


**Outcome.** The first table of money. Vendors are ranked descending by spend with shares paired to
absolutes and a totals row that visibly reconciles, so the number can be checked by eye rather than
trusted. Long lists cap with an explicit remainder rather than silently truncating, which keeps the
arithmetic closed at every level of detail.

---

## Run 15 — Anthropic collector ✅

**Needs:** `ANTHROPIC_ADMIN_KEY`.

**Build:** the second collector against the now-proven interface. **This is the run that tells you whether
the abstraction was right** — if it fights, we fix the interface now, while there are only two.

**Do this**
```bash
make check
export ANTHROPIC_ADMIN_KEY=<your admin key>
./aispend doctor
rm -rf /tmp/as7 && AISPEND_HOME=/tmp/as7 ./aispend scan --since 7d
AISPEND_HOME=/tmp/as7 ./aispend usage
```

**Success criteria**
1. `make check` clean.
2. `doctor` reports anthropic reachable with workspace and key counts.
3. Both vendors collect in the same `scan`, concurrently, and the report shows both.
   The design §6.1 progress block appears while it works — `✓ openai  3 projects · 28 keys  1.2s`,
   one line per vendor with its own timing — and is suppressed when stdout is not a TTY.
4. Spot-check two Anthropic facts against their console — they agree.
5. **Three input classes, three columns.** Anthropic reports uncached input, cache *writes* (priced at a
   premium) and cache *reads* (priced at a discount). Schema v1 had one `cached_units`, so run 15 adds
   migration v2 with `cache_write_units`. Combining any two moves the number in opposite directions and
   the errors do not cancel — this is the design's cached-tokens rule, discovered to have one more case
   than the design anticipated.
6. One vendor failing does not abort the other — kill the Anthropic key and openai still reports, with a
   clear per-vendor error.


**Outcome.** A second vendor collects alongside the first, and the collector abstraction either
holds or gets fixed here, while there are only two implementations to reconcile. Concurrency and
per-vendor failure isolation become observable: one vendor's bad credential produces a clear message for
that vendor while the other still reports, rather than aborting the run. The progress block from the
design appears now that there is more than one thing to wait for.

---

## Run 16 — price book and `amount_basis` ✅

**Build:** embedded effective-dated `pricebook.json`, consulted **only** where the vendor doesn't report
cost. Allocation of coarse vendor cost down to model level, `price_version` stamped on computed facts,
and the `Basis` footer that splits reported from allocated.

**Do this**
```bash
make check
go test ./internal/pricing/ -v
./aispend usage --fixture testdata/
sqlite3 /tmp/as7/aispend.db "select amount_basis, count(*), sum(amount_micros) from usage_fact group by amount_basis;"
```

**Success criteria**
1. `make check` clean.
2. Footer reads `Basis  $12,880 vendor-reported · $2,246 allocated to model (price book 2026.08)`.
3. Those two figures **sum to the reported total**.
4. Vendor-reported amounts are never overwritten by computed ones.
5. An unknown model emits a fact with `amount_basis='unknown'` and a visible warning — **not a silent
   $0**.
6. Effective dating works: a fact dated before a price change uses the older entry. Test asserts this.


**Outcome.** The tool can explain where its numbers came from. Vendor-reported cost is never
overwritten; the effective-dated price book is consulted only where a vendor doesn't report money, and
what it computes is stamped with its version and counted separately. The `Basis` footer surfaces that
split, and it is the single line that decides whether a finance person forwards the report or quietly
discards it. An unknown model produces a visible warning rather than a silent zero.

---

## Run 17 — `BY MODEL` and the rest of `--by` ✅

**Build:** the model table, plus the remaining dimensions from design §6.3 — `--by key`, `--by project`,
`--by day`. They share one grouping path, so building them together is cheaper than building one and
retrofitting three. (`--by team` waits for run 20, which needs `owners.csv`.)

**Do this**
```bash
make check
./aispend usage --fixture testdata/
./aispend usage --fixture testdata/ --by model
./aispend usage --fixture testdata/ --by key
./aispend usage --fixture testdata/ --by project
./aispend usage --fixture testdata/ --by day
./aispend usage --fixture testdata/ --by model --detail
```

**Success criteria**
1. `make check` clean.
2. Model table shows spend and tokens, descending by spend.
3. Default view caps at ~5 rows with `…and 9 more  $3,818`, and the remainder **reconciles** to the total.
4. `--detail` shows every row with no truncation.
5. Tokens use the humanised format; `--json` (run 22) will use exact integers.
6. **Every `--by` dimension totals to the same number.** Group by model, by key, by project, by day — the
   totals row is identical every time. If they disagree, the grouping is dropping or duplicating rows,
   and that is the bug that destroys the tool's credibility fastest.
7. `--by key` shows **truncated** key identifiers (`sk-…a4f2`), never a full key.
8. `--by day` is ordered chronologically, not by spend — it's a time series, and the descending-by-cost
   rule doesn't apply to it.


**Outcome.** The report gains depth. Model, key, project and day views share one grouping path, so
they arrive together and — critically — all reconcile to the same total, which is the arithmetic a
sceptical reader checks first. Key identifiers appear truncated, and the day view orders chronologically
rather than by cost, because a time series sorted by size is not a time series.

---

## Run 18 — sparklines and deltas

**Build:** 7-day sparkline per vendor, prior-window delta with the window named, deltas under 5% muted as
noise, and the ASCII fallback **in the same run** — or it gets forgotten.

**Do this**
```bash
make check
./aispend usage --fixture testdata/
LC_ALL=C ./aispend usage --fixture testdata/
./aispend usage --fixture testdata/ | cat
```

**Success criteria**
1. `make check` clean.
2. Header shows `▲ 34%  vs prior 30d` — signed, with the comparison window **named**.
3. A sparkline appears per vendor row, 7 buckets.
4. `LC_ALL=C` falls back to `+34%` and `.:|#` with alignment intact.
5. A delta under 5% is visibly muted, not shouted.
6. A vendor with fewer than 7 days of data draws a partial sparkline, not a padded-with-zeros lie.


**Outcome.** Trend becomes visible at a glance. Each vendor row carries a seven-day sparkline and
the header carries a delta against the prior window with that window named, so a change is never reported
without saying what it is a change from. Deltas under five percent are muted as the noise they are, and
the ASCII fallback ships in the same run rather than being deferred and forgotten.

---

## Run 19 — OpenRouter collector

**Needs:** an OpenRouter key. The easiest of the three, with the best granularity available anywhere.

**Do this**
```bash
make check
export OPENROUTER_API_KEY=<key>
./aispend doctor
./aispend scan --since 7d
./aispend usage
```

**Success criteria**
1. `make check` clean.
2. Three vendors in `doctor`, three in the report.
3. Per-request granularity is preserved down to model level.
4. Total scan time for all three stays under ~10s for a 7-day window.
5. Vendor mix percentages still sum to 100%.


**Outcome.** The third vendor completes the v1 set, and the vendor mix in the report becomes the
real picture of a customer's API spend rather than a partial one. OpenRouter's per-request granularity is
the best available anywhere, which makes it a useful confidence check on the aggregation the other two
collectors depend on.

---

## Run 20 — attribution · **the run that demos the thesis**

**Build:** optional `~/.aispend/owners.csv`, `--by team`, and the loud **Unattributed** line. No mapping
UI. A prospect seeing `Unattributed  $12,024  (79%)  31 keys` has understood the product without you
saying a word.

**Do this**
```bash
make check
./aispend usage --fixture testdata/            # no owners.csv yet
cp testdata/owners.example.csv ~/.aispend/owners.csv
./aispend usage --fixture testdata/ --by team
./aispend usage --fixture testdata/            # attribution block now splits
printf 'garbage,,,\n' >> ~/.aispend/owners.csv && ./aispend usage --fixture testdata/
```

**Success criteria**
1. `make check` clean.
2. With no `owners.csv`: everything lands in **Unattributed**, shown with amount, percentage and key count.
3. With the file: `--by team` groups correctly and mapped + unmapped **sum to the total**.
4. Comments (`#`) and blank lines in the CSV are tolerated.
5. A malformed row produces a warning naming the **line number**, and the rest of the file still loads.
6. `owners.csv` is never written to by the tool — it is the user's file, read-only.


**Outcome.** This is the run that demonstrates the product thesis without a word of pitch. An
optional CSV the user drops in maps principals to teams; if it is absent, everything lands in a loudly
displayed **Unattributed** line, and a prospect reading "Unattributed $12,024 (79%)" has just understood
what the product is for. There is no mapping UI, no import flow and no schema for the user to learn — a
file that may or may not exist, and a number that is compelling either way.

---

## Run 21 — the surprise engine

**Build:** four rules from design §6.1, each a query plus a threshold plus a sentence. Ranked, top three
shown. The whole validation thesis is whether the number surprises them, so compute the surprises rather
than hoping they're spotted.

**Do this**
```bash
make check
go test ./internal/analytics/ -v
./aispend usage --fixture testdata/
./aispend usage --fixture testdata/ --since 7d
```

**Success criteria**
1. `make check` clean.
2. `⚠ 3 things worth a look` with three specific, numeric sentences — each naming the figures behind it.
3. Rules are ranked; changing the window changes which rules fire.
4. Nothing fires on data where nothing is unusual — the block is **absent**, not empty-with-a-header.
5. Each rule has a unit test with a fixture that triggers it and one that doesn't.
6. No rule names a prompt, a user, or a full key — truncated identifiers only.
7. The report ends with the design §6.1 `Next` line — `aispend usage --by team   aispend export --share
   aispend purge` — the three commands that continue the conversation on a call. Cheap, and it's what
   turns a printed number into the next five minutes of a demo.


**Outcome.** The report starts pointing at what matters instead of leaving it to be noticed. Four
ranked rules compute the surprises explicitly — a week-over-week jump, a single key dominating spend,
premium models on trivial requests — and the top three are shown with the figures behind them. The whole
validation exercise turns on whether the number surprises the reader, so it is computed rather than hoped
for. The closing `Next` line turns a printed report into the next five minutes of a conversation.

---

## Run 22 — machine-readable output

**Do this**
```bash
make check
./aispend usage --fixture testdata/ --json | jq .
./aispend usage --fixture testdata/ --json | jq '.totals'
./aispend usage --fixture testdata/ --csv | head -5
./aispend export --csv > /tmp/spend.csv && open /tmp/spend.csv
./aispend usage --fixture testdata/ --json | grep -i "key\|secret\|token=" || echo "no creds — good"
```

**Success criteria**
1. `make check` clean.
2. `--json` is valid JSON (`jq` parses it) with **raw integer micros**, not formatted strings.
3. Token counts in JSON are exact integers, not humanised.
4. `--csv` opens cleanly in a spreadsheet with a header row.
5. JSON and CSV totals equal the human report's total.
6. No credential material in either format, in any field.


**Outcome.** The report leaves the terminal. JSON carries raw integer micros and exact token counts
for anything downstream, CSV opens in a spreadsheet, and both reconcile to the human report's total.
Neither format carries credential material in any field, which is checked rather than assumed.

---

## Run 23 — `connect`, `disconnect`, `purge`

**Build:** masked key entry via `x/term`, **immediate verification** before storing, keychain via
`go-keyring`. `disconnect` offers to keep or drop collected data. `purge` deletes everything and prints
exactly what it removed.

This is the run where you'll get an OS keychain password prompt.

**Do this**
```bash
make check
./aispend connect openai          # paste a key at the masked prompt
./aispend connections
unset OPENAI_ADMIN_KEY && ./aispend doctor      # works from keychain alone
security find-generic-password -s aispend 2>&1 | head -3   # macOS
./aispend disconnect openai
./aispend purge
ls -la ~/.aispend 2>&1
```

**Success criteria**
1. `make check` clean.
2. The key is **not echoed** while typing, and not in your shell history.
3. A bad key is **rejected before being stored** — verification happens first.
4. With the env var unset, `doctor` still works: the credential came from the keychain.
5. `connection.keyring_ref` in SQLite holds a lookup reference, **never the secret**. Check it.
6. `disconnect` removes the keychain entry and asks about the data rather than assuming.
7. `purge` prints an itemised list of what it removed, and `~/.aispend` is gone afterwards along with
   every keychain entry.


**Outcome.** The stateful posture arrives, and with it the two commands that buy trust. `connect`
takes a key at a masked prompt, verifies it immediately, and stores it in the OS keychain — encrypted at
rest by the operating system, and an answer a security reviewer already accepts — while the database
holds only a lookup reference. `purge` deletes the database and every keychain entry and prints exactly
what it removed, which is what lets you say "when you're done, run this and every trace is gone" before
the objection is raised.

---

## Run 24 — `export --share`

**Build:** the shape-not-amounts block from design §6.4, plus the single optional `surprised?` prompt at
the end of `scan` — one keystroke, skippable, and literally the metric this whole exercise measures.

**Do this**
```bash
make check
./aispend export --share
./aispend export --share | grep '\$' || echo "no absolute amounts — good"
./aispend scan --since 7d      # answer the prompt
./aispend scan --since 7d < /dev/null   # non-interactive
```

**Success criteria**
1. `make check` clean.
2. The block contains ratios, counts and percentages — **no absolute dollar amounts**, no key
   identifiers, no project names.
3. It's copy-pasteable as plain text into an email.
4. The `surprised` prompt is one keystroke and skippable with Enter.
5. Non-interactive invocation **skips the prompt entirely** rather than hanging. Test this one properly —
   a tool that blocks in CI is a tool that gets uninstalled.


**Outcome.** You learn something without the tool transmitting anything. `export --share` produces a
block of shape rather than amounts — vendor mix, model count, unattributed percentage, period change —
that most people will paste into a reply without hesitation, which is the only way to get aggregate
signal across ten prospects from a binary that phones nobody. The single skippable `surprised?` prompt is
literally the metric the exercise exists to measure.

---

## Run 25 — errors and the redacting writer

**Build:** every failure gets the what/why/fix shape from design §6.6, plus a redacting `io.Writer` on
stdout and stderr that scrubs credential-shaped strings — **including in panic output**.

**Do this**
```bash
make check
go test ./internal/redact/ -v
OPENAI_ADMIN_KEY=sk-clearly-invalid-key ./aispend doctor
OPENAI_ADMIN_KEY=sk-test-abc ./aispend debug panic 2>&1 | grep -c "sk-test-abc" || echo "redacted — good"
./aispend scan --since 999d
./aispend usage --by nonsense
```

**Success criteria**
1. `make check` clean.
2. A planted credential in a **panic** does not reach the terminal — asserted by a test, not by eye.
3. Every error states what happened, what it means, and the exact command that fixes it.
4. No raw HTTP body, no stack trace without `--debug`.
5. Nonsense flags produce a helpful message naming the valid values, not a usage dump.
6. Exit codes are non-zero on failure — pipelines can tell.


**Outcome.** Failure becomes a designed surface rather than an accident. Every error states what
happened, what it means and the exact command that fixes it, no raw HTTP body or stack trace reaches the
terminal without `--debug`, and a redacting writer on stdout and stderr scrubs credential-shaped strings
as a last line of defence — including in panic output, asserted by a test with a planted credential
rather than by inspection. This is the run that separates a binary a stranger persists with from one they
abandon after the first confusing message.

---

## Run 26 — README and release

**Build:** README with the **security section above the install instructions** (it's what gets the binary
run), cross-compilation to 5 targets, checksums, `cosign` signatures, GitHub Release.

**Do this**
```bash
make check
make release          # builds all targets into dist/
ls -la dist/
shasum -c dist/checksums.txt
GOOS=linux GOARCH=amd64 go build .    # cgo-free cross-compile, no toolchain needed
```

**Success criteria**
1. `make check` clean.
2. Five binaries: darwin/arm64, darwin/amd64, linux/amd64, linux/arm64, windows/amd64.
3. Cross-compilation needs **no C toolchain** — this is the whole reason for `modernc.org/sqlite`.
4. Checksums verify.
5. Binary is a single file under ~20 MB with no runtime dependencies.
6. README's first substantive section is security, and it is honest about what an admin key can do
   beyond reading — an observed read call is not scope introspection, and some vendors' admin keys can
   do more than read. Say it plainly rather than letting a security reviewer discover it.
7. `LICENSE` is present (MIT or Apache-2.0) and the repo is public. Readable source is a stronger
   security claim than anything you can write about it, and this binary is the seed of the open-source
   collector.
8. The `curl | sh` installer exists but the README **leads with the direct download** — plenty of the
   orgs you want ban piping the internet into a shell, and leading with it signals you don't know your
   buyer.

**Outcome.** The binary becomes something a stranger will actually run. The README leads with
security above the install instructions, because that is what decides whether the download happens, and
it is honest about the limits of what `doctor` can observe rather than leaving a reviewer to discover
them. Five targets cross-compile from one machine with no C toolchain — the payoff for the storage
decision in run 4 — with checksums and signatures, and the direct download leads over the shell
installer, because plenty of the organisations worth selling to ban piping the internet into a shell.

---

## Standing checks — run any time

```bash
make check
grep -rn "telemetry\|posthog\|segment\|amplitude" --include="*.go" . | grep -v _test.go   # must stay empty
sqlite3 ~/.aispend/aispend.db "select * from connection;"              # never a secret
./aispend scan --dry-run                                               # every host it can reach
```

---

## Definition of done

From design §12, unchanged:

1. Download → a number on screen in **under 60 seconds**, no config file, no documentation.
2. The total **reconciles to your vendor invoices within 2%**, and where it doesn't, `Basis` explains why.
3. Nothing identifying a person, prompt or key is anywhere in the DB; `grep -ri` finds no telemetry endpoint.
4. `purge` leaves no trace and says what it removed.
5. **Someone who is not you ran it, unaided, on a machine you did not set up.**

Then send it to ten people and count how many say the number surprised them. That count — not the code —
is the output of this week.
