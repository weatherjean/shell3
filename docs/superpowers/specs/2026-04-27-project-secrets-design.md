# Project Secrets Store

Date: 2026-04-27
Status: Design

## Problem

Tool secrets currently live in `<cwd>/.shell3/.env` as plaintext. The
adapter credential store at `~/.shell3/credentials.shell3` is XOR-wrapped
(`config.Wrap`/`Unwrap`). Secrets should get the same at-rest treatment,
project-scoped, and managed by a dedicated CLI surface — separate from
auth.

The `destroy` subcommand exists to delete `.shell3/`, but `rm -rf .shell3`
is just as clear and one less command to maintain.

## Goals

1. Replace `.shell3/.env` plaintext with `<cwd>/.shell3/secrets.shell3`,
   wrapped using the same scheme as the credential store.
2. Add `shell3 secrets {set,list,remove}` for management. Project-only;
   refuses outside an inited shell3 project.
3. Drop the `destroy` subcommand. Document `rm -rf .shell3` in README.
4. Hard cutover. No `.env` migration. No backwards compatibility.

## Non-Goals

- Per-project credential overrides. Auth stays global at `~/.shell3/`.
- Encryption (the XOR wrap is obfuscation, not crypto). Same trust model
  as the credential store.
- Cross-project secret sharing.

## Architecture

### Storage

New file: `<cwd>/.shell3/secrets.shell3`

- Plaintext payload: YAML.
  ```yaml
  version: 1
  secrets:
    BRAVE_API_KEY: sk-abc123xyz
    OPENWEATHER_API_KEY: 9f8...
  ```
- Wrapped on disk using `config.Wrap` (XOR + version byte, same as
  credentials).
- File mode `0600`. Directory `0700`.
- Atomic write via `*.tmp` rename.

### Package: `internal/secrets`

New package, mirrors `internal/config/credstore.go` shape:

```go
package secrets

type Store struct { /* projectDir, mu, data */ }

func Load(projectDir string) (*Store, error)         // returns empty store if file absent
func (s *Store) Set(key, value string) error          // overwrites; persists
func (s *Store) Remove(key string) error              // no-op if absent; persists
func (s *Store) Get(key string) (string, bool)        // for runtime tool exec
func (s *Store) List() []string                        // sorted key names only
func (s *Store) All() map[string]string               // copy; for runtime injection
```

Persistence + wrapping reuse `config.Wrap`/`Unwrap`. The package depends
on `internal/config` for those two helpers; no circular dep risk.

`Load` requires `<projectDir>/.shell3/` to exist. If absent it returns
an error (`shell3 init` not run). This enforces the "inited project
only" rule at the storage layer rather than the CLI layer.

### CLI: `cmd/shell3/secrets.go`

```
shell3 secrets set --key NAME --secret VALUE
shell3 secrets list
shell3 secrets remove --key NAME
```

Both `--key` and `--secret` are required on `set`. No prompt fallback.
No `get` command (printing values defeats the obfuscation purpose).

`list` masks values: shows only the **last 3 characters** of each value
replaced with `***` — i.e. for value `abc123xyz`, list prints
`abc123***`. Each line:

```
NAME                  abc123***
```

Bare `shell3 secrets` prints help; no interactive picker (auth has one
because adapters are a closed set; secret keys are open and tool-defined).

The cobra root's `PersistentPreRun` (added in the prior change) prints
the brand header for this command like every other subcommand.

### Runtime wiring (`cmd/shell3/run.go`)

Replace lines 101-116 (the dotenv + `os.Environ` merge) with:

```go
secStore, err := secrets.Load(cwd)
if err != nil {
    return fmt.Errorf("load secrets: %w", err)
}
secretsMap := secStore.All()
available := map[string]struct{}{}
for k := range secretsMap {
    available[k] = struct{}{}
}
```

The `os.Environ()` overlay is dropped. Secrets reaching tools come
**only** from the secrets store. Tools that declare `secrets: [NAME]`
in their YAML resolve names against this map; missing names disable
the tool with a warning (existing behavior in
`usertools.LoadAll`).

### Scaffold (`internal/scaffold/scaffold.go`)

- Drop `.env.example` write.
- Drop `.env` references in the brave_search example tool's
  `description` field. Update text to say "run `shell3 secrets set
  --key BRAVE_API_KEY --secret <token>` to enable."
- Add `secrets.shell3` to `defaultGitignore`.
- The existing `.gitignore` line for `.env` is removed (no longer
  exists).

Updated `defaultGitignore`:

```
# shell3 runtime files — do not commit
shell3.db
memory.db
history.md
last_error.json
secrets.shell3
```

### Removals

- `cmd/shell3/destroy.go` — deleted.
- `newDestroyCommand()` registration in `cmd/shell3/main.go` — removed.
- `internal/usertools/dotenv.go` — deleted along with its test.
- `LoadDotEnv` references in `cmd/shell3/run.go` — removed.

### README update

Add a short section near the install/init notes:

> ### Removing a project's shell3 data
> `rm -rf .shell3` from the project root. There is no `shell3 destroy`
> command.

## Data flow

```
shell3 secrets set --key BRAVE_API_KEY --secret xxx
  → secrets.Load(cwd)
  → store.Set("BRAVE_API_KEY", "xxx")
  → Wrap + atomic write to <cwd>/.shell3/secrets.shell3

shell3            (run)
  → secrets.Load(cwd)
  → store.All() → secretsMap
  → tools loaded with availability set from secretsMap keys
  → tool exec: env passed includes only secretsMap[k] for keys
    declared in the tool's `secrets:` field
```

## Error cases

| Case                                  | Behavior                                           |
|---------------------------------------|----------------------------------------------------|
| `secrets set` outside inited project  | Error: "no .shell3/ here — run `shell3 init`"      |
| `secrets remove` for missing key      | Silent no-op (matches credstore.Delete)            |
| `secrets list` empty                  | Print "No secrets configured."                     |
| `secrets.shell3` corrupt              | `Load` returns wrap error; CLI surfaces it         |
| `secrets.shell3` permissions != 0600  | Warn on stderr, continue                           |

## Testing

- `internal/secrets`: unit tests mirroring `internal/config/credstore_test.go`
  — set/get/list/remove, persistence round-trip, wrap interop.
- `cmd/shell3` integration: build binary, run `secrets set`, then
  `secrets list` (assert masked output), then `secrets remove`, then
  `secrets list` (assert empty).
- Update `internal/scaffold/scaffold_test.go` to reflect dropped
  `.env.example` and `.env` gitignore line, added `secrets.shell3`
  gitignore line.
- Delete `internal/usertools/dotenv_test.go`.

## Open questions

None — all resolved during brainstorm.

## Out of scope (future work)

- Per-project credentials (auth scoped per repo).
- Encrypted secrets (real KMS / OS keychain integration).
- `shell3 secrets import .env` migration.
- A `secrets get --key NAME` reveal command (would need extra confirmation).
