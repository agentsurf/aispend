# aispend

Provider-agnostic AI spend analytics, from the command line. One binary, no
runtime, no account, no server.

```
$ aispend scan --since 30d

  AI SPEND · last 30 days · 29 Jul – 27 Aug 2026

  Total                                           $522.81
                                            ▲ 41% vs prior 30 days

  BY VENDOR
  Anthropic                                $386.57      74%   ███████
  OpenRouter                                $78.46      15%   ▇██▆▇██
  OpenAI                                    $57.78      11%   ▇███▇▇█
  ─────────────────────────────────────────────────────────
                                           $522.81

  ATTRIBUTION
  Mapped to a team                         $259.06      85%
  Unattributed                              $47.35      15%   3 keys

  ⚠  3 things worth a look
     · one key (apik…hmiT) is 64% of everything ($198.08)
     · anthropic up 116% in the second half of this range ($75.05 → $162.50)
     · claude-sonnet-4-6 cost $39.48 at a premium rate on small workloads

  ──────────────────────────────────────────────────────────────
  Basis     $444.35 computed from the price book · $78.46 vendor-reported
  Privacy   Nothing left this machine. Contacted: api.anthropic.com, api.openai.com
  Days      All dates UTC. Vendors restate recent days, so the last 2 may change.
```

---

## Security

This section is above the install instructions on purpose. You are being asked
to run an unfamiliar binary against an admin credential, and you should want
this answered first.

### It cannot contact anything but your vendors

The list of permitted hosts is compiled into the binary and enforced **in the
network dialer**, not in a policy some code path might forget to consult. No
collector, no future feature and no mistake can open a connection to a host that
is not in the vendor catalog.

```
$ aispend scan --dry-run
  would GET api.openai.com/v1/organization/usage/completions ×30
  would GET api.anthropic.com/v1/organizations/usage_report/messages ×30

  aispend is structurally incapable of contacting any other host.
  Permitted: api.anthropic.com, api.openai.com, openrouter.ai
```

`--dry-run` makes no request at all. Run it with your network disconnected and
the output is identical.

### There is no telemetry

Not opt-out. Absent from the source. Check for yourself:

```
grep -rn "telemetry\|posthog\|segment\|amplitude" --include="*.go" .
```

### Your credentials are never written to disk by aispend

Keys come from environment variables, or from your operating system's credential
store if you run `aispend connect`. They are never written to the database,
never to a config file, never to any output format, and never to a log. The
database has no column that could hold one.

The credential type redacts itself in every formatting verb, so a key cannot
leak through a `%v`, a wrapped error, or a panic. A redacting writer wraps
stdout and stderr as a last line of defence. There is a test that panics while
holding a credential and asserts it does not reach the terminal.

### What the credential can do is broader than what aispend does

Be clear about this, because a security reviewer will find it if you do not.

aispend issues `GET` requests to two usage endpoints per vendor and nothing
else. But the credential you hand it is more powerful than that:

- **Anthropic Console Admin keys have no selectable scopes.** A single key can
  read usage *and* manage organisation members, workspaces and API keys.
- **OpenAI Admin keys** likewise cover the whole organisation endpoint surface.

`aispend doctor` reports what a credential could actually read by making one
cheap read call. That is an *observed* result, not scope introspection — aispend
cannot tell you what else a key is permitted to do, and does not pretend to.

If that is not acceptable, do not use an admin key: the Anthropic Usage and Cost
endpoints also accept a personal key that is not scoped to a workspace.

### You can delete everything, and see what was deleted

```
$ aispend purge
  This will permanently delete:

    ~/.aispend/aispend.db  (4,182 facts, 2 connections)
    Anthropic's key in your OS keychain
    ~/.aispend/owners.csv
    ~/.aispend  (the directory itself)
```

### The source is the strongest claim

MIT licensed, public from day one. Four direct dependencies:

| Dependency | For |
|---|---|
| `github.com/spf13/cobra` | commands and help |
| `modernc.org/sqlite` | storage, pure Go, no cgo |
| `github.com/zalando/go-keyring` | OS credential store |
| `golang.org/x/term` | masked input, TTY detection |

---

## Install

Download the binary for your platform from the releases page, verify the
checksum, and run it. There is no installer and nothing to configure.

```
curl -LO https://github.com/prabhuvmk/aispend/releases/latest/download/aispend_darwin_arm64
shasum -a 256 -c checksums.txt --ignore-missing
chmod +x aispend_darwin_arm64 && mv aispend_darwin_arm64 /usr/local/bin/aispend
```

A `curl | sh` installer also exists, but the direct download is listed first
deliberately: plenty of organisations do not allow piping the internet into a
shell, and the shorter command is not worth the argument.

Building from source needs Go 1.25 or later and no C toolchain:

```
go build -o aispend .
```

---

## Use

The shortest path to a number involves no configuration:

```
export ANTHROPIC_ADMIN_KEY=sk-ant-admin01-...
export OPENAI_ADMIN_KEY=sk-...
aispend scan
```

That collects, reports, and exits. Nothing is required beyond the environment
variables — no setup, no config file, no account.

Then explore what was collected, offline and instantly:

```
aispend usage --by team       # needs ~/.aispend/owners.csv
aispend usage --by model
aispend usage --by key
aispend usage --since 7d
aispend usage --vendor anthropic
aispend export --csv > spend.csv
```

### Commands

| Command | Network | What it does |
|---|:--:|---|
| `scan` | ✓ | Collect from every connected vendor, then report |
| `sync` | ✓ | Collect without printing a report |
| `usage` | | Report from what has already been collected |
| `export` | | Write JSON, CSV, or a shareable summary |
| `doctor` | ✓ | Diagnose paths, database, credentials and reachability |
| `connections` | | List supported vendors and their status |
| `connect` | ✓ | Store a key in your OS keychain, after verifying it |
| `disconnect` | | Remove a key, and choose what happens to the data |
| `purge` | | Delete everything, and say what was deleted |

### Vendors

| Vendor | Credential | Where |
|---|---|---|
| OpenAI | Admin key | Settings → Organization → Admin keys |
| Anthropic | Admin key | Settings → Admin keys |
| OpenRouter | API key | Keys |

All three require an **organisation** account. Individual and personal plans
cannot access the usage APIs these reports are built from, so aispend has
nothing to read on them.

### Attribution

Drop a CSV at `~/.aispend/owners.csv` and `--by team` works:

```csv
vendor,principal_ref,team,cost_center
openai,proj_a91f,platform,ENG-101
anthropic,apikey_01Rj2N,search,ENG-104
```

Without it, everything appears under **Unattributed** — which is usually the
more interesting report.

---

## How the numbers are produced

**Money is integer micros throughout.** No floating point touches an amount
between the vendor's response and your screen.

**`Basis` says where each figure came from.** "The vendor told us this cost
$8,204" and "we computed $3,266 from a price book" are different claims, and the
footer keeps them apart. OpenRouter reports cost per model directly; OpenAI and
Anthropic report cost more coarsely than usage, so their model-level figures are
computed from an effective-dated price book and labelled as such.

**Unknown is never zero.** A figure aispend could not determine renders as an em
dash with a footnote. A tool that shows "we could not see this" as `$0` has
silently lied.

**All dates are UTC**, stored as `YYYY-MM-DD`, converted only at display time.

**Vendors restate recent days.** A restatement is stored as a new revision
rather than overwriting, so the earlier figure stays on disk and "the number
changed because the vendor restated on the 14th" is an answer.

**Cached tokens are priced separately** — reads at a discount, writes at a
premium. Folding them into ordinary input tokens produces an error that grows as
you optimise, which is exactly when you would be checking.

---

## Files

```
~/.aispend/                 0700
├── aispend.db              0600   collected usage. No credentials, ever.
├── owners.csv              0600   optional, yours
├── config.toml             0600   optional
└── raw/                    0700   only with --keep-raw
```

## Licence

MIT.
