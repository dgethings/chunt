# Plan: Definition-aware diagnostics + Definition / References / DocumentSymbol

Goal: turn the parser's coverage into real LSP semantic features. Track definitions
of named entities (route-map, access-list, class-map, policy-map, vlan, interface),
resolve references to them, and surface unresolved/duplicate/unused references as
diagnostics. Implement Go-To-Definition, Find-References, and Document Symbol
along the way.

Scope: single-file resolution only. Cross-file / workspace is a later round.

## Current state

- Parser (`../tree-sitter-cisco-ios-jinja2/grammar.js`) covers most IOS commands
  via flat `command_line` nodes plus rich rules for ~50 high-frequency keywords.
- Hierarchical sections exist only for `interface_section` and `router_section`.
- `route-map`, `class-map`, `policy-map`, `vlan`, `ip access-list`, `line`,
  `redundancy` are explicitly DEFERRED as section nodes (see comment blocks at
  `grammar.js:620-623` and `grammar.js:886-933`). They currently parse flat.
- LSP server (`internal/server/`) declares Hover, Definition, Completion.
  Capabilities live at `internal/server/initialize.go:19-30`; method dispatch at
  `internal/server/initialize.go:46-56`.
- The `Feature` interface (`internal/features/feature.go:10`) has no `References`
  or `DocumentSymbol` methods.
- `internal/features/cisco_ios_jinja2/definition.go:10` is a stub returning the
  previous line.
- `internal/features/cisco_ios_jinja2/diagnostics.go:13` has one rule
  (running-vs-configured version mismatch).
- No IR / symbol table exists between tree-sitter and the LSP.

## Phase 1 — Grammar: model the deferred sections

Edit `../tree-sitter-cisco-ios-jinja2/grammar.js`. Each new section mirrors the
existing `interface_section` / `router_section` shape: a `*_header` rule with a
named `field("name", ...)`, a body of `repeat(choice($._nl, $._body_item))`, and
a terminating `$.eos`.

| New node | Header | `name` field | Body rules (already in `_command`) |
| --- | --- | --- | --- |
| `route_map_section` | `route-map NAME (permit\|deny) SEQ` | NAME | `match_statement`, `set_statement`, `continue_statement` |
| `class_map_section` | `class-map [match-any\|match-all] NAME` | NAME | `match_statement`, `description_statement` |
| `policy_map_section` | `policy-map NAME` | NAME | `class_statement`, `police_statement`, `priority_statement`, `drop_statement`, `random_detect_statement` |
| `vlan_section` | `vlan N` | N | `private_vlan_statement`, `remote_span_statement`, `name ...` |
| `ip_access_list_section` | `ip access-list (standard\|extended) NAME` | NAME | `permit_statement`, `deny_statement` |
| `line_section` | `line (aux\|console\|vty) N [M]` | N | the 16 config-line `*_statement` rules |
| `redundancy_section` | `redundancy` | (singleton) | `auto_sync_statement` |

Implementation notes:

- Promote the header keywords (`route-map`, `class-map`, `policy-map`,
  `ip access-list`, `line`, `redundancy`) to `token(prec(2, ...))` so they win
  over `value`. This is the change called out as the natural next step at
  `grammar.js:925-933`.
- Watch the `class` vs `class-map` longest-match trap already documented at
  `grammar.js:906-916`: register `class_map_section` ahead of `class_statement`
  in any dispatch list, give the header its own longer `token(prec(2, "class-map"))`.
- Keep the existing duplicated registrations in `_ios_statement` as a graceful
  fallback for malformed configs. Harmless and avoids corpus regressions.
- Numbered ACLs (`access-list 101 permit ...`) stay as flat `command_line` —
  they are one-line definitions, not sections. The symbol extractor (Phase 2)
  handles both forms.
- Add corpus cases per new section under
  `../tree-sitter-cisco-ios-jinja2/test/corpus/`. Run `make test` there
  (regenerates `src/parser.c`, `src/grammar.json`, `src/node-types.json` and
  runs the corpus). Commit `src/` and `grammar.js` together.
- `chunt`'s Makefile will auto-regen the Go binding via `.ts-gen-stamp`
  on the next `make test-lsp`.

## Phase 2 — Symbol table (new `internal/symbols/` package)

```go
type Kind string
const (
    KindInterface Kind = "interface"
    KindRouteMap  Kind = "route-map"
    KindClassMap  Kind = "class-map"
    KindPolicyMap Kind = "policy-map"
    KindVlan      Kind = "vlan"
    KindACL       Kind = "acl"     // covers both named and numbered
    KindLine      Kind = "line"
)

type Symbol struct {
    Kind      Kind
    Name      string
    URI       string
    Range     protocol.Range   // full header line
    NameRange protocol.Range   // just the name token (Definition / highlight)
}

type Table struct { /* keyed by URI, then by Kind+Name */ }
func (t *Table) Index(uri string, tree *sitter.Tree, content []byte)
func (t *Table) Lookup(kind Kind, name string) []Symbol
func (t *Table) LookupAny(name string) []Symbol
func (t *Table) All(uri string) []Symbol
func (t *Table) Clear(uri string)
```

Extraction strategy: replace the unused placeholder at
`internal/features/cisco_ios_jinja2/queries.go:3` with real tree-sitter queries
that capture `(..._header name: @name)` for each section kind, plus a small
walker for flat numbered ACLs (`access-list N permit|deny ...`). Index after
every parse in `DidOpen` / `DidChange`.

Wire into `CiscoIOSFeature` (`feature.go:16`): add `symbols *symbols.Table`,
call `Index` after each parse, `Clear(uri)` in `DidClose`. Single-file scope
means the table can be a value field — no locking needed (server serializes
per-document).

## Phase 3 — Reference walker (new file in `internal/features/cisco_ios_jinja2/`)

Data-driven table of "reference introducers":

```go
type refSpec struct {
    Leading  []string     // leading keyword tokens
    ArgIndex int          // 0-based offset of the referenced name in trailing args
    Kind     symbols.Kind // expected definition kind
}
var refSpecs = []refSpec{
    {[]string{"ip", "access-group"}, 0, KindACL},
    {[]string{"access-class"}, 0, KindACL},
    {[]string{"match", "ip", "address"}, 0, KindACL},
    {[]string{"ip", "policy", "route-map"}, 0, KindRouteMap},
    {[]string{"service-policy"}, 1, KindPolicyMap}, // service-policy input NAME
    {[]string{"class"}, 0, KindClassMap},           // inside policy-map body
    {[]string{"switchport", "access", "vlan"}, 0, KindVlan},
    {[]string{"switchport", "trunk", "allowed", "vlan"}, 0, KindVlan},
    // neighbor ... route-map NAME — overloaded; skip in v1
}
```

Walker iterates every `command_line` and rich-rule node, reads leading keyword
tokens + `arg` children, matches against `refSpecs`, emits `{Kind, Name, Range}`.

## Phase 4 — Diagnostics

1. Add severity constants to `internal/protocol/types.go` (`SeverityError=1`,
   `SeverityWarning=2`, `SeverityInformation=3`, `SeverityHint=4`). Add
   `DiagnosticTag` constants (`Unnecessary=1`, `Deprecated=2`) and
   `RelatedInformation` to `Diagnostic` for "see definition at …" jumps.
2. Split `diagnostics.go` into `diagnostics_version.go` (existing rule) and
   `diagnostics_refs.go` (new). Add `runUndefinedReferenceDiagnostics(doc, tree,
   symbols)`: for each reference, if `symbols.Lookup(kind, name)` is empty, emit
   `SeverityWarning` with message `"undefined route-map 'FOO'"`, anchored at the
   reference's `NameRange`.
3. Optional v1 additions: duplicate-definition warnings (same kind+name defined
   twice → warning on the second); unused-definition hints
   (`SeverityHint` + `DiagnosticTag:[Unnecessary]`) — gate behind a flag.

Order: version mismatch → undefined refs → duplicates → unused.

## Phase 5 — Real Definition

Replace the stub at `internal/features/cisco_ios_jinja2/definition.go:10`:

1. `ast.FindNodeAtPosition` to get the cursor node.
2. Walk up to the enclosing `value` / `identifier` token (the reference name).
3. Determine expected `Kind` from the parent command's leading tokens via the
   Phase 3 `refSpecs` table.
4. `symbols.Lookup(kind, name)` → return the def's `Location`. Fall back to
   `LookupAny(name)` if no kind hint.
5. If the cursor is on a definition's `NameRange`, return that location
   (definition of itself).

## Phase 6 — References LSP

1. Extend `Feature` interface with
   `References(ctx, doc, pos) ([]protocol.Location, error)`.
2. Add `internal/protocol/references.go` (`ReferenceParams`, etc.).
3. Advertise `ReferencesProvider: true` in `ServerCapabilities`
   (`internal/server/initialize.go:19-30`) and add the `textDocument/references`
   entry to the assigner map (`initialize.go:46-56`).
4. Add `internal/server/references.go` dispatcher mirroring
   `internal/server/definition.go`.
5. Implement `CiscoIOSFeature.References`: resolve the cursor to a symbol
   (def or ref) → return every reference `Range` matching `(kind, name)`.

## Phase 7 — DocumentSymbol LSP

1. Extend `Feature` interface with
   `DocumentSymbol(ctx, doc) ([]protocol.DocumentSymbol, error)`.
2. Add `internal/protocol/document_symbol.go` (`DocumentSymbol`, `SymbolKind`
   constants: Interface=8, Class=5, Namespace=3 for ACLs/route-maps, Number=12
   for vlans).
3. Advertise `DocumentSymbolProvider: true`; add handler.
4. Implement: iterate `symbols.All(uri)`, build a `DocumentSymbol` per section
   with name, kind, range, and `selectionRange = NameRange`. Optionally nest
   body statements under their parent section.

## Phase 8 — Tests

- **Grammar** — corpus files in `../tree-sitter-cisco-ios-jinja2/test/corpus/`
  per new section. `make test-ts`.
- **Symbols** — `internal/symbols/symbols_test.go`: feed sample configs, assert
  indexed names per kind.
- **Diagnostics** — `internal/features/cisco_ios_jinja2/diagnostics_test.go`:
  cases for missing route-map, missing ACL, missing policy-map, duplicate
  definition, unused definition. Mirror the `completion_test.go` pattern.
- **Definition / References / DocumentSymbol** — table-driven tests mirroring
  `hover_test.go`.
- `make test-lsp` (CGO required).

## Suggested implementation order

1. Phase 1 grammar (blocking — no robust extraction without `name` fields).
2. Phase 2 symbol table + indexing.
3. Phase 3 reference walker.
4. Phase 4 undefined-reference diagnostics (the stated goal).
5. Phase 5 real Definition (small once symbols exist).
6. Phases 6–7 References and DocumentSymbol (plumbing once 2–5 work).

## Out of scope (flag for later)

- `keywords.go` duplication and missing `config-router` section data.
- `document.New()` not populating `Document.Lines`; `OffsetAt` unsafe to call.
- Cross-file references (would require a per-server symbol index).
