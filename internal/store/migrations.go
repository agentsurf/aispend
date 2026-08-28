package store

// Schema v1. A deliberate subset of the full product schema that migrates
// forward cleanly, rather than a different design.
//
// Two things here differ from the design's §5 listing, both for the same reason
// — SQLite lets NULL into a PRIMARY KEY, and NULL never equals NULL:
//
//  1. workspace_ref, principal_ref and model_ref are NOT NULL DEFAULT ”.
//     Left nullable, two collections of the same fact with no principal would
//     not conflict, and re-running a scan would double the row. Empty string
//     means "the vendor does not report this dimension"; the renderer shows it
//     as an em dash, never as a zero.
//  2. Tables are STRICT, so amount_micros cannot quietly become a float. The
//     integer-money rule stops being a convention the code has to remember.
var migrations = []migration{
	{
		version: 1,
		stmts: `
CREATE TABLE connection (
  vendor        TEXT PRIMARY KEY,
  account_ref   TEXT NOT NULL DEFAULT '',
  label         TEXT NOT NULL DEFAULT '',
  cred_source   TEXT NOT NULL,             -- 'env' | 'keyring'
  keyring_ref   TEXT NOT NULL DEFAULT '',  -- lookup key, NEVER the secret
  connected_at  INTEGER NOT NULL,
  last_ok_at    INTEGER
) STRICT;

CREATE TABLE usage_fact (
  fact_id        TEXT    NOT NULL,          -- sha256 of the dimensions, stable across machines
  vendor         TEXT    NOT NULL,
  day            TEXT    NOT NULL,          -- 'YYYY-MM-DD', UTC. Never local time.
  workspace_ref  TEXT    NOT NULL DEFAULT '',
  principal_ref  TEXT    NOT NULL DEFAULT '',
  model_ref      TEXT    NOT NULL DEFAULT '',

  input_units    INTEGER NOT NULL DEFAULT 0,
  output_units   INTEGER NOT NULL DEFAULT 0,
  cached_units   INTEGER NOT NULL DEFAULT 0,
  other_units    INTEGER NOT NULL DEFAULT 0,
  unit_kind      TEXT    NOT NULL,

  amount_micros  INTEGER NOT NULL,          -- USD x 1e6. Integers only, never floats.
  amount_basis   TEXT    NOT NULL,
  price_version  TEXT    NOT NULL DEFAULT '',

  revision       INTEGER NOT NULL DEFAULT 1,
  collected_at   INTEGER NOT NULL,

  PRIMARY KEY (vendor, day, workspace_ref, principal_ref, model_ref, revision),

  CHECK (vendor <> ''),
  CHECK (length(day) = 10),
  CHECK (unit_kind <> ''),
  CHECK (revision >= 1),
  CHECK (amount_basis IN ('vendor_reported','allocated','computed','unknown'))
) STRICT;

CREATE INDEX idx_fact_day ON usage_fact(day);
CREATE INDEX idx_fact_id  ON usage_fact(fact_id);

CREATE TABLE sync_state (
  vendor        TEXT PRIMARY KEY,
  covered_from  TEXT NOT NULL DEFAULT '',
  covered_to    TEXT NOT NULL DEFAULT '',
  cursor        TEXT NOT NULL DEFAULT '',   -- opaque, vendor-specific
  last_run_at   INTEGER,
  last_error    TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE TABLE meta (k TEXT PRIMARY KEY, v TEXT NOT NULL) STRICT;
`,
	},
}

type migration struct {
	version int
	stmts   string
}

// schemaVersion is the version a fresh database is created at.
func schemaVersion() int { return migrations[len(migrations)-1].version }
