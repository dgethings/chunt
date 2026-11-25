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

Six families of diagnostics, all anchored precisely on the offending token:

1. **Syntax / parse** (Error / Warning) — tree-sitter `ERROR` nodes (tokens that could not be incorporated into any rule, e.g. an unterminated `{% if %}`) are reported as errors quoting the offending snippet; `MISSING` tokens are reported naming what is missing. A section missing its terminating `!` is downgraded to a Warning anchored on the section header.
2. **Version mismatch** (Error) — when the `! version X` comment emitted by `show run` disagrees with the configured `version Y` statement.
3. **Undefined reference** (Warning) — when a command names a target that has no definition anywhere in the file's directory group (see [Multi-file templates](#multi-file-templates-directory--one-device-config)).
4. **Duplicate definition** (Warning) — when the same `(kind, name)` is defined twice; the diagnostic carries `relatedInformation` back to the first definition.
5. **Argument value** (Error) — when an argument the keyword database types as a specific value kind holds something that is not one: subnet masks (`ip address 10.0.0.1 wibble` → `expected a subnet mask (e.g. 255.255.255.0), got "wibble"`), wildcard masks, IPv6 addresses and prefixes, MAC addresses, AS numbers, VLAN IDs, durations (`seconds`/`milliseconds`/`minutes`), and curated IPv4 slots (`ip address`, `ip route`'s prefix, `ip nat outside source static`'s addresses). Driven by the keyword-DB snippet placeholders (`${N:mask}`, `${N:vlan-id}`, …) — two registries: unambiguous names globally, ambiguous names like `ip-address` only on curated address-only commands (IOS accepts hostnames in several address slots). Jinja-templated values (`{{ ... }}`) and optional-`[...]`-slot bindings are skipped; see `diagnostics_args.go`.
6. **Repeated / redefined commands** (Warning / Error) — duplicate or conflicting practice: an identical command appearing twice in one logical section (`duplicate-command` Warning, e.g. a copy-pasted static route) and scalar redefinitions where the last definition wins (`redefined-setting` **Error**) — `hostname r1` then `hostname r2`, multiple primary `ip address` lines, conflicting `router-id` values, interface settings (`description`, `mtu`, `bandwidth`, `speed`, `duplex`, `encapsulation`, `switchport mode/access vlan`, `vrf forwarding`), global settings (`banner`, `clock timezone/summer-time`, `ip domain-name`, `enable secret`+`password` both present, `snmp-server location/contact`, `ip default-gateway`, `ip ssh version`, `logging buffered/source-interface`), FHRP group scalars (`standby|vrrp|glbp <grp> ip/priority/preempt/authentication`), and routing-process/BGP scalars (`timers bgp`, `distance bgp`, `maximum-paths`, `neighbor <addr> remote-as/description/update-source/password`). `secondary` lines are additive and exempt; negations clear the tracked state, so remove-then-re-add is fine; commands inside Jinja blocks are out of scope. Both carry `relatedInformation` back to the first definition — across files when running in directory mode — and a cross-file conflict additionally marks the first definition with a companion `superseded-setting` Warning naming the replacing value and its file; see `diagnostics_repeated.go`.
7. **Subnet overlap** (Error / Warning) — `duplicate-subnet` (**Error**): the same exact subnet on two interfaces (`10.1.1.1 255.255.255.0` on Gi0/0 and `10.1.1.9 255.255.255.0` on Gi0/1 — different text, same subnet); `overlapping-subnet` (**Warning**): one subnet strictly inside another (`10.1.1.0/24` vs `10.1.1.128/25`); `duplicate-virtual-ip` (**Warning**): the same HSRP/VRRP/GLBP virtual IP on two interfaces or twice on one interface in different groups. Secondary addresses, `dhcp`/`unnumbered`, Jinja-computed addresses, and interfaces in different VRFs are exempt; see `diagnostics_subnets.go`.
8. **Unused definition** (Hint) — a named definition of a referenceable kind that nothing in scope references (`unused-acl`, `unused-vrf`, `unused-route-map`, …). Conservative: Jinja-guarded definitions and computed names never flag, and an unresolved `{{ }}` reference of a kind counts as a use of every definition of that kind; see `diagnostics_unused.go`.

User-facing codes:

| code | severity | example |
| --- | --- | --- |
| `undefined-<kind>` | Warning | `undefined acl "ACL-OUT"` — acl (v4/v6/mac/arp), route-map, class-map (incl. typed inspect), policy-map, vlan, prefix-list, community-list, as-path-acl, vrf, track, time-range, nat-pool, crypto-map, transform-set, isakmp-policy/profile, ipsec-profile, zone, parameter-map, peer-group, interface |
| `duplicate-<kind>` | Warning | `duplicate vrf definition "BLUE"` — same kind set plus hostname; seq-addressed kinds (prefix-list entries, crypto-map entries) key on `(name, seq)` |
| `unused-<kind>` | Hint | `acl "ORPHAN" is never referenced; the definition has no effect` |
| `duplicate-command` | Warning | `duplicate command "ip route 10.0.0.0 255.0.0.0 Gi0/0"` |
| `redefined-setting` | Error | `banner motd "^Authorized only^" replaces "^Welcome to r1^"; only the last definition takes effect` |
| `superseded-setting` | Warning | `router-id "1.1.1.1" is superseded by "2.2.2.2" defined later in b-bgp.ios.j2` |
| `duplicate-subnet` | Error | `subnet 10.1.1.0/24 is configured on both "Gi0/0" and "Gi0/1"` |
| `overlapping-subnet` | Warning | `subnet 10.1.1.128/25 ("Gi0/1") is contained in subnet 10.1.1.0/24 ("Gi0/0")` |
| `duplicate-virtual-ip` | Warning | `standby virtual ip 10.1.1.254 is duplicated: group 1 on "Gi0/0" and group 1 on "Gi0/1"` |

Well-known names that never have explicit definitions (`class class-default`, the implicit `global`/`Mgmt-vrf` VRFs, the community-list `internet` keyword) are suppressed; numbered community-list references still flag — a typo'd number is a real catch.

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

Line endings are a non-issue: CRLF content (a Windows editor saving an
`.ios.j2` file) is normalized to LF in chunt's document layer before
parsing, so diagnostic ranges, hover, completion, definition, and
references behave identically for CRLF and LF buffers.

### Multi-file templates (directory = one device config)

A network device's configuration is commonly split across several template
files — base config, routing, ACLs. chunt treats every `*.ios.j2` file in the
**same directory** as ONE logical device config (one device type per
directory):

- **Sections may be split across files.** `router ospf` in one file and
  `router bgp` in another are simply distinct sections; the same section
  identity (e.g. `router bgp 65001`) appearing in several files merges
  silently into one logical section.
- **Conflicting definitions are still errors.** Two different `router-id`
  values under the same logical `router bgp` section — even in different
  files — is a `redefined-setting` Error anchored on the later definition
  (merge order = sorted filename), with a jump back to the first. The
  FIRST definition's file also gets a companion `superseded-setting`
  Warning anchored on it (naming the replacing value and the file it moved
  to), so both files show their participation in the conflict. Identical
  duplicated lines across files are `duplicate-command` Warnings.
- **References resolve across the whole group.** `ip access-group MGMT in`
  in one file is satisfied by `ip access-list ... MGMT` in another.

Discovery is server-side and editor-agnostic: opening or editing any file
rescans its directory (one `stat` per sibling; changed files re-read by
mtime). Files open in the editor stay authoritative; closed siblings are
re-read from disk; diagnostics are published for background files too, so
cross-file findings show in the editor's Problems panel without opening
those files. When the last file in a directory closes, the group is torn
  down and its published diagnostics cleared.

Known v1 limitations: no subdirectory recursion (a device is ONE directory),
section identity is literal header text (`interface {{ name }}` merges only
with identical template text), and cross-file conflicts anchor the Error on
the later definition (the first gets the companion Warning described
above).

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

Pre-built binaries are published on the [releases page](https://github.com/dgethings/chunt/releases) for darwin, linux, and windows on amd64 and arm64 (linux and windows cross-compiled with zig; windows binaries are plain `.exe` console executables).

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

```text
chunt serve                  # run as an LSP server over stdio (the default mode for editors)
chunt check <file|directory> # one-off diagnostic run; prints in compiler-output format
chunt version                # print the version
```

`chunt check` on a single file loads its directory siblings as read-only
context (cross-file rules apply; diagnostics anchored in the checked file
are printed). On a directory it checks every `*.ios.j2` inside
(non-recursive) as one logical device config and prints every file's
anchored diagnostics.

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
│   │       ├── diagnostics_args.go    # snippet-driven argument values (invalid-mask)
│   │       ├── diagnostics_repeated.go # duplicate-command + redefined-setting warnings
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
