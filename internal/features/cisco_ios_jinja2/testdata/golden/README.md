# Golden integration fixtures

This directory holds table-driven **golden-file** integration tests for the
Cisco IOS + Jinja2 LSP feature set. Each fixture feeds a realistic config
through the *full* feature pipeline (parse → `symbols.Index` → every
diagnostic pass, plus `Completion` / `Hover` / `Definition` / `References` /
`DocumentSymbol` at marked cursor positions) and compares the output against a
checked-in `.golden` file. A deliberate
regression therefore surfaces as a readable diff in the test output, and
regenerating the goldens surfaces behavioral changes as a PR diff.

The harness lives at
[`internal/features/cisco_ios_jinja2/golden_test.go`](../../../internal/features/cisco_ios_jinja2/golden_test.go)
(`TestGolden`). It is an **additive integration tier** — the inline unit tests
in the sibling `*_test.go` files are kept; this suite wires them together
end-to-end and doubles as integration coverage for
[`internal/symbols`](../../../internal/symbols) extraction.

## Fixture layout

Each fixture is a name with up to three files:

| file             | required | purpose                                                                              |
| ---------------- | -------- | ------------------------------------------------------------------------------------ |
| `<name>.cfg`     | yes      | the input config — raw, **no inline markers**, so line/column offsets are exactly what a real editor sends |
| `<name>.marks`   | no       | cursor positions for `Completion` / `Hover` (see below); omit if the fixture only checks diagnostics |
| `<name>.golden`  | yes      | the expected pipeline output — **generated**; never hand-edit                         |

A fixture with only a `.cfg` is perfectly valid (diagnostics-only).

## The `.marks` file

One cursor position per line, **0-indexed** (LSP `Position` semantics):

```text
# blank lines and lines starting with '#' are ignored
4:0 hover       hostname keyword -> docstring
47:1 completion config-if scope (inside the interface body)
1:18 definition on the acl reference "ACL1" -> <none> when undefined
0:11 references-decl from a definition -> every usage plus declarations
0:0 documentSymbol full outline (cursor is irrelevant; 0:0 is the placeholder)
```

Format: `<line>:<col> <feature> [free-form note…]`. `<feature>` is one of:

| feature           | feature call                                                  |
| ----------------- | ------------------------------------------------------------- |
| `completion`      | `Completion` at the position                                   |
| `hover`           | `Hover` at the position                                        |
| `definition`      | `Definition` at the position                                   |
| `references`      | `References` at the position, `includeDeclaration=false`        |
| `references-decl` | `References` at the position, `includeDeclaration=true`         |
| `documentSymbol`  | `DocumentSymbol` for the whole document — position is ignored; convention is `0:0` |

The optional trailing text is echoed into the golden header as a
human annotation — handy for explaining *why* a position is interesting.

### How cursor-sensitive output is rendered

* **Diagnostics** — full deterministic dump, sorted by `(line, column, code,
  severity)` so the golden is stable regardless of pass ordering:

  ```text
  WARNING  undefined-acl   1:17-21 | undefined acl "ACL1"
  ```

  A diagnostic's `RelatedInformation` is appended as
  `~> <uri> <range> "<message>"`. An empty diagnostic set renders an explicit
  `<none>`.

* **Hover** — `kind=<markupkind>` then the docstring value (bounded, so it is
  diffable and meaningful — it is the actual contract). `nil` renders `<none>`.

* **Completion** — the keyword database is large (a single section yields
  thousands of items with very long labels), so dumping every label verbatim
  would make goldens enormous and churn on every keyword-DB edit. Instead each
  mark records the item **count** plus a **sha256 fingerprint** of the sorted
  unique label set:

  ```text
  2718 items  sha256:601eef186a455ebe
  ```

  A section-resolution regression (cursor resolving to the wrong section)
  changes both the count and the fingerprint; a keyword-DB content change
  changes the fingerprint. Re-run `-update` and read the `git diff` to see what
  moved. Picking marks in *different* section scopes (e.g. `config-if`,
  `config-vlan`, global `config`) makes the wiring signal obvious — each scope
  yields a distinct count.

* **Definition / References** — one `<file> <range>` per line, sorted by
  `(uri, range start, range end)` so the golden is stable regardless of the
  order the symbol table yields (`References` appends declarations after the
  reference sites; sorting normalizes that). An empty result renders an
  explicit `<none>` — the interesting case for an undefined name
  (`undefined_refs.cfg`) or a definition with no usages:

  ```text
  undefined_refs.cfg 1:17-21
  <none>
  ```

  Pairs of `references` / `references-decl` marks at the same position make
  the includeDeclaration split visible in the diff. A reference to a name with
  duplicate definitions resolves to **all** of them (`duplicates.cfg`) — that
  cross-feature interaction is exactly what this tier guards.

* **DocumentSymbol** — the outline rendered as a flat, indented tree, one
  symbol per line: `kind=<num>/<Name> <name> [detail=<detail>] <range>
  sel=<selectionRange>`. Siblings stay in document order (the order
  `DocumentSymbol` returns); children are rendered recursively (empty today).
  The `0:0 documentSymbol` mark renders the whole outline regardless of the
  cursor:

  ```text
  kind=11/Interface GigabitEthernet0/0 detail="interface" 46:0-53:1 sel=46:10-28
  ```

## Adding a fixture

1. Drop a `<name>.cfg` into this directory. Keep it **small and targeted at one
   pass** (the design notes call for one pass per fixture so failures stay
   localized): e.g. `version_mismatch.cfg` exercises only the version pass.
   `clean_all_sections.cfg` is the exception — it is the broad "kitchen sink"
   that covers every section kind + Jinja constructs and asserts a clean
   (zero-diagnostic) baseline.
2. *(optional)* Add a `<name>.marks` with cursor positions.
3. Regenerate the golden:

   ```bash
   go test ./internal/features/cisco_ios_jinja2/ -run '^TestGolden/<name>$' -update
   ```

4. **Inspect** the generated `<name>.golden` and confirm it says what you
   expect — `-update` captures *current* behavior, which is only correct if the
   behavior itself is. A golden that records a bug freezes the bug.
5. Commit `<name>.cfg` (+ `.marks`) and `<name>.golden` together.

## Regenerating all goldens

```bash
go test ./internal/features/cisco_ios_jinja2/ -run TestGolden -update
```

The normal test run (`make test-lsp`, i.e. `go test ./...`) compares without
touching the files. A mismatch fails with a unified-style diff naming the
changed lines.

## Notes on the keyword database

The keyword database (keywords.go) is noisy: common words such as `match`,
`name`, `priority`, and `mode` resolve to obscure sections
(`config-domain-vrf-mc-class`, `config-ipv6-pmipv6-domain-mn`, …) and so emit
spurious `wrong-section` Hints even in their correct context.

The canonical router-process commands `network` and `router-id` were the
highest-impact case of this (every OSPF/BGP `network` line is a false positive),
and are now suppressed via a curated overlay in `keyword_overlay.go`
(`keyword.Set.AddValidSections`), which marks them valid in `config-router`
without altering hover/completion. Other commands remain noisy. The
diagnostic-only fixtures below are deliberately built from **noise-free**
commands so each fixture's golden contains only the diagnostics of its targeted
pass. When authoring a new clean/diagnostic fixture, prefer the noise-free
commands demonstrated by the existing fixtures (or run the config and read the
golden to see what the DB emits).
