# chunt

A [Language Server Protocol](https://microsoft.github.io/language-server-protocol/) implementation for **Cisco IOS configuration files with Jinja2 templating** (`.ios.j2`).

chunt parses `running-config` style files and templates and surfaces structural errors, missing definitions, and cross-reference issues that the IOS CLI itself won't flag until deploy time. It also provides hover documentation, completion, go-to-definition, find-references, and document outlines for editors that speak LSP.

Powered by a dedicated [tree-sitter-cisco-ios-jinja2](https://github.com/dgethings/tree-sitter-cisco-ios-jinja2) grammar.

---

## Features

| LSP method | What it does |
| --- | --- |
| `textDocument/publishDiagnostics` | Flags version mismatches, undefined references, and duplicate definitions as you type. |
| `textDocument/hover` | Documentation for the keyword under the cursor, pulled from a built-in IOS command database. |
| `textDocument/completion` | Section-aware keyword completion (`config` vs `config-if` vs `config-router`). |
| `textDocument/definition` | Jump to the definition of a referenced route-map / ACL / class-map / policy-map / vlan / interface. |
| `textDocument/references` | Find every place a named entity is used (and optionally its declaration). |
| `textDocument/documentSymbol` | Outline view of every section header in the file. |

### Diagnostics

Four families of diagnostics, all anchored precisely on the offending token:

1. **Syntax / parse** (Error / Warning) — tree-sitter `ERROR` nodes (tokens that could not be incorporated into any rule, e.g. an unterminated `{% if %}`) are reported as errors quoting the offending snippet; `MISSING` tokens are reported naming what is missing. A section missing its terminating `!` is downgraded to a Warning anchored on the section header.
2. **Version mismatch** (Error) — when the `! version X` comment emitted by `show run` disagrees with the configured `version Y` statement.
3. **Undefined reference** (Warning) — when a command names a target that has no definition in the same file.
4. **Duplicate definition** (Warning) — when the same `(kind, name)` is defined twice; the diagnostic carries `relatedInformation` back to the first definition.

Example:

```bash
$ chunt check site-a.ios.j2
site-a.ios.j2:1:1:  [chunt] section "interface GigabitEthernet0/0" is missing its terminating "!"
site-a.ios.j2:5:18:  [chunt] undefined acl "ACL-OUT"
site-a.ios.j2:6:22:  [chunt] undefined route-map "RM-OUT"
site-a.ios.j2:9:19:  [chunt] undefined acl "ACL-VOICE"
site-a.ios.j2:13:11: [chunt] duplicate route-map definition "RM1"
site-a.ios.j2:20:1:  [chunt] running version and configured version mismatch
```

Same diagnostics show up as red/yellow squiggles in any LSP-aware editor.

### Entities tracked

chunt builds a per-file symbol table for the following IOS named entities and resolves references between them:

| Kind | Definition sites | Reference introducers |
| --- | --- | --- |
| `interface` | `interface NAME` | (planned) |
| `router` | `router bgp N` / `router ospf N` | (planned) |
| `route-map` | `route-map NAME permit\|deny SEQ` | `ip policy route-map NAME` |
| `class-map` | `class-map [match-any\|match-all] NAME` | `class NAME` (in policy-map body) |
| `policy-map` | `policy-map NAME` | `service-policy <dir> NAME` |
| `vlan` | `vlan N` | `switchport access vlan N` |
| `line` | `line (aux\|console\|vty) ...` | (planned) |
| `redundancy` | `redundancy` | — |
| `acl` | `ip access-list <standard\|extended> NAME` and `access-list N permit\|deny ...` | `ip access-group NAME`, `access-class NAME`, `match ip address NAME` |

Built-in names (currently just `class-default`) are suppressed so they don't generate false positives.

### Completion and hover

Completion is sourced from a built-in database of ~6,000 IOS commands, segmented by config mode:

- `config` (global)
- `config-if` (inside an `interface` section)
- `config-router` (inside a `router` section)
- `config-line` (inside a `line` section)

Hover returns the same description text in plaintext form. Typing a keyword with arguments suppresses completion so value entry isn't interrupted.

---

## Installation

### From source

```bash
git clone https://github.com/dgethings/chunt
cd chunt
make                                    # builds bin/chunt
```

The tree-sitter grammar is a normal Go dependency (published at [github.com/dgethings/tree-sitter-cisco-ios-jinja2](https://github.com/dgethings/tree-sitter-cisco-ios-jinja2)); `make` fetches it from the module proxy — **no sibling checkout is required to build or test**. CGO is required (tree-sitter is C); the Makefile sets `CGO_ENABLED=1` automatically.

Bump the grammar version:

```bash
make grammar-bump                        # to @latest
make grammar-bump GRAMMAR_VERSION=v0.3.1  # to a specific tag
```

The version is the `require` line in `go.mod`; `chunt version` also prints it.

**Contributing to the grammar alongside chunt:** clone the sibling repo next to chunt and run `make workspace` to write a machine-local, gitignored `go.work` that points chunt at your local grammar tree (normal clone or git-worktree checkout — both are auto-detected) instead of the published module:

```bash
git clone https://github.com/dgethings/tree-sitter-cisco-ios-jinja2 ../tree-sitter-cisco-ios-jinja2
make workspace                           # opt-in local override (writes go.work)
make grammar                             # rebuild the local grammar binding
make test-grammar                        # run the grammar's tree-sitter corpus
```

Remove `go.work` (or `make clean-workspace`) to revert to the published module.

### Binary

Pre-built binaries are published on the [releases page](https://github.com/dgethings/chunt/releases) for darwin/linux on amd64 and arm64.

---

## Editor integration

chunt speaks LSP over stdio. Configure it like any other language server.

### Neovim (builtin LSP)

```lua
-- Register the filetype (the .j2 part is significant)
vim.filetype.add({
  pattern = { [".*%.ios%.j2"] = "cisco_ios_jinja2" },
})

vim.lsp.config("chunt", {
  cmd = { "/path/to/chunt", "serve" },
  filetypes = { "cisco_ios_jinja2" },
  root_markers = { ".git" },
})
vim.lsp.enable("chunt")
```

For syntax highlighting via tree-sitter (separate from the LSP), install the [tree-sitter-cisco-ios-jinja2](https://github.com/dgethings/tree-sitter-cisco-ios-jinja2) grammar with your plugin manager of choice, or copy the queries from this repo's `queries/` directory into your runtimepath.

### VS Code

Install a generic LSP client extension (e.g. [Generic LSP](https://marketplace.visualstudio.com/items?itemName=antonkaschenko.generic-lsp)) and point it at `chunt serve` for the `cisco_ios_jinja2` language ID.

### Helix

```toml
# ~/.config/helix/languages.toml
[language-server.chunt]
command = "chunt"
args = ["serve"]

[[language]]
name = "cisco_ios_jinja2"
scope = "source.ios.j2"
file-types = [{ suffix = ".ios.j2" }]
language-servers = [ "chunt" ]
```

---

## CLI usage

`chunt` has three subcommands:

```
chunt serve                  # run as an LSP server over stdio (the default mode for editors)
chunt check <file>           # one-off diagnostic run; prints in compiler-output format
chunt version                # print the version
```

Global flag:

```
--log-level <debug|info|warn|error>   # default: info, sent to stderr
```

### CI / pre-commit

`chunt check` exits 0 on a clean file and prints diagnostics (exit 0 — it does not fail the run unless you wrap it). A typical pre-commit hook:

```bash
#!/bin/sh
# Run chunt over every templated config in the repo
status=0
for f in $(find . -name '*.ios.j2'); do
  if ! chunt check "$f" | grep -q .; then :; else
    chunt check "$f"
    status=1
  fi
done
exit $status
```

---

## Example session

Given this template with three intentional mistakes:

```jinja
!
hostname r1
!
interface GigabitEthernet0/0
 ip access-group ACL-OUT in
 ip policy route-map RM-OUT
!
class-map match-any VOICE
 match ip address ACL-VOICE
!
policy-map QOS
 class VOICE
  priority
!
service-policy input QOS
!
```

- `ACL-OUT` has no matching `ip access-list ... ACL-OUT` or `access-list ...` definition.
- `RM-OUT` has no matching `route-map RM-OUT ...` definition.
- `ACL-VOICE` (referenced by the class-map) is also undefined.
- `QOS` is correctly defined and referenced.

```bash
$ chunt check site-a.ios.j2
site-a.ios.j2:5:18:  [chunt] undefined acl "ACL-OUT"
site-a.ios.j2:6:22:  [chunt] undefined route-map "RM-OUT"
site-a.ios.j2:9:19:  [chunt] undefined acl "ACL-VOICE"
```

In an editor with LSP integration, the same information shows up as inline squiggles, and:

- Hovering over `ACL-OUT` shows nothing extra (no docs for user-defined names) but Go-To-Definition is disabled because there is no target.
- Defining `ip access-list standard ACL-OUT` somewhere in the file makes the warning disappear.
- From a defined `ACL-OUT`, `chunt` Find-References returns every `ip access-group ACL-OUT`, `access-class ACL-OUT`, and `match ip address ACL-OUT` site.
- The document outline lists `r1` (router), `GigabitEthernet0/0` (interface), `VOICE` (class-map), `QOS` (policy-map) as top-level entries.

---

## Project layout

```
chunt/
├── cmd/                          # cobra CLI: serve, check, version
├── internal/
│   ├── ast/                      # tree-sitter node helpers
│   ├── document/                 # document model + in-memory store
│   ├── features/
│   │   └── cisco_ios_jinja2/     # the actual LSP feature implementations
│   │       ├── completion.go
│   │       ├── hover.go
│   │       ├── definition.go
│   │       ├── references.go
│   │       ├── document_symbol.go
│   │       ├── diagnostics.go          # dispatcher
│   │       ├── diagnostics_syntax.go   # tree-sitter ERROR / MISSING tokens
│   │       ├── diagnostics_version.go  # version mismatch rule
│   │       ├── diagnostics_refs.go     # undefined refs + duplicates
│   │       ├── diagnostics_section.go  # wrong-section hint
│   │       └── keywords.go             # ~6k IOS command DB
│   ├── keyword/                  # Keyword type + Lookup / InSection
│   ├── protocol/                 # hand-rolled LSP JSON-RPC DTOs
│   ├── server/                   # jrpc2 method handlers
│   └── symbols/                  # per-URI symbol + reference table
├── main.go
├── Makefile                      # builds both this repo and the sibling grammar
└── go.mod                        # replace directive points at ../tree-sitter-cisco-ios-jinja2
```

The tree-sitter grammar lives in a sibling repo: [tree-sitter-cisco-ios-jinja2](https://github.com/dgethings/tree-sitter-cisco-ios-jinja2).

---

## Development

```bash
make              # build bin/chunt (also regenerates the grammar binding if grammar.js changed)
make test-lsp     # CGO_ENABLED=1 go test -race ./...
make test-grammar # tree-sitter corpus test in the sibling grammar repo (needs the checkout)
make test         # test-lsp only (Go suite); run test-grammar separately for the corpus
make cover        # go test -race -cover; prints per-package + total, fails below COVER_MIN (default 75)
make cover-html   # regenerate cover.out and open an HTML report in the browser
make snapshot     # local goreleaser snapshot build
```

**Coverage gate.** `make cover` runs the CGO-enabled suite with `-race -cover`,
prints `go tool cover -func` (per-package + `total:`), and exits non-zero when
the total falls below the `COVER_MIN` floor (default `75`; measured total is
~75.7%). Override the floor for a single run with `make cover COVER_MIN=80`, or
bump the default in the `Makefile` to ratchet it upward. The floor is total-only
by design — per-package floors are deferred to avoid churn. The `keyword`
package (~92%) sets the high-water mark.

**Release gate.** Every release (and `make snapshot`) is gated on green tests
and the coverage floor: `.goreleaser.yml` runs `make test` then `make cover` as
`before` hooks before any artifact is built, so a failing test or a coverage
regression aborts the release before publishing. No sibling grammar checkout is
needed — both targets use the published module, so the gate holds in a clean
checkout or CI.

**Golden integration tests.** The LSP feature package has a table-driven
golden-file suite (`TestGolden`) that feeds realistic configs through the full
pipeline — parse, `symbols.Index`, every diagnostic pass, plus `Completion` /
`Hover` at marked cursor positions — and compares the output against checked-in
`.golden` files. It runs as part of `make test-lsp` (no extra target); a
deliberate regression fails with a unified-style diff naming the changed
lines. Regenerate the goldens after an intentional behavior change:

```bash
go test ./internal/features/cisco_ios_jinja2/ -run TestGolden -update
```

See
[`internal/features/cisco_ios_jinja2/testdata/golden/README.md`](internal/features/cisco_ios_jinja2/testdata/golden/README.md)
for the fixture format and how to add one.

See [PLAN.md](PLAN.md) for the multi-phase design that landed the current feature set, and [PLAN-IP-ACCESS-LIST.md](PLAN-IP-ACCESS-LIST.md) for the next planned grammar refinement.

---

## License

MIT — see [LICENSE](LICENSE).
