# Plan: ip_access_list_section (Phase 1 follow-up)

Goal: model `ip access-list <standard|extended> NAME { ... }` as a real
hierarchical section so the AST matches IOS semantics, and replace the
Phase 2 flat-AST extraction hacks with header-based name lookup.

Phase 1 deferred this section because promoting `ip` (or `access-list`) to a
prec-2 keyword collides with every other `ip ...` line. This plan resolves
the collision via the same `_cmd_arg` alias technique Phase 1 already uses
for `route-map` / `vlan` / `class-map` / `policy-map` / `line` /
`redundancy`.

## Current state (verified)

```sh
$ tree-sitter parse - <<'EOF'
ip access-list standard FOO
 permit 10.0.0.0 0.0.0.255
!
access-list 101 permit ip any any
EOF
```

```
(config
  (command_line (identifier)     ; "ip"
                arg: (value)     ; "access-list"
                arg: (value)     ; "standard"
                arg: (value))    ; "FOO"   <-- named ACL header is FLAT
  (permit_statement ...)                  <-- body is a SIBLING, not a child
  (eos)
  (command_line (identifier))             ; "access"
  (text "-list 101 permit ip any any"))   <-- numbered ACL header is BROKEN
```

Consequences:

- ACL body lines (`permit ...` / `deny ...`) are not nested under their
  parent ACL. They are top-level statements.
- Numbered ACL parsing is split across `command_line(access)` and a sibling
  `text` node. Phase 2 has a regex-based workaround
  (`internal/symbols/symbols.go: numberedACLRe`) that recovers the number
  from the text content. This is fragile.
- ACL definitions are extracted by walking flat `command_line` nodes and
  pattern-matching leading identifier + first arg — works, but it is a
  special case outside the clean `sectionSpec` table.

Functionally, ACL def/ref resolution ALREADY WORKS today (verified):

```
$ chunt check acl-demo.ios.j2
acl-demo.ios.j2:9:18: [chunt] undefined acl "ACL-MISSING"
```

So this plan is a structural / hygiene improvement, not a feature gap.

## Design

### Grammar (../treesitter-cisco-ios-jinja2/grammar.js)

1. **Promote `access-list` to a named prec-2 token**, parallel to
   `route_map_kw` / `vlan_kw` / etc:

   ```js
   access_list_kw: $ => token(prec(2, "access-list")),
   ```

2. **Add it to `_cmd_arg` via alias** (the Phase 1 lexer-commit
   workaround) so command_line can still consume `access-list` as an
   arg in lines like `ip access-list standard FOO` if the GLR parser
   chooses that branch:

   ```js
   _cmd_arg: $ => choice(
     $.value,
     $.output,
     alias($.route_map_kw, $.value),
     alias($.class_map_kw, $.value),
     // ... existing entries ...
     alias($.access_list_kw, $.value),
   ),
   ```

3. **Add the named ACL section + header**:

   ```js
   ip_access_list_section: $ => seq(
     $.ip_access_list_header,
     repeat(choice($._nl, $._body_item)),
     $.eos,
   ),

   // `ip access-list <standard|extended> NAME`. The leading `ip` is a
   // bare identifier (NOT promoted — see Phase 1's deferred note about
   // `ip` causing +344 errors). The parser disambiguates via GLR: at
   // line start of `ip access-list standard FOO` both command_line(ip)
   // and ip_access_list_header are tried; the second token's
   // tokenization decides — lexer produces access_list_kw (prec 2),
   // which command_line's arg position accepts via alias BUT which
   // ip_access_list_header expects directly. Header rule has higher
   // precedence via prec.right, so it wins.
   ip_access_list_header: $ => prec.right(seq(
     $.identifier,                                        // "ip"
     $.access_list_kw,                                    // "access-list"
     field("type",  choice($.value, $.output)),          // "standard" | "extended"
     field("name",  choice($.value, $.output)),
   )),
   ```

4. **Add a numbered-ACL flat statement** so the `text` companion goes
   away and Phase 2's regex hack can be deleted:

   ```js
   // `access-list <N> permit|deny ...`. Replaces the old
   // `command_line(access) + text(-list ...)` form. The leading keyword
   // is the prec-2 access_list_kw, so command_line (which starts with
   // identifier) cannot match — every access-list line at line start
   // now routes here. Body args (number, action, ACE specifics) stay
   // unstructured; downstream resolution is the LSP's job.
   access_list_statement: $ => prec.right(seq(
     $.access_list_kw,
     repeat(field("arg", $._cmd_arg)),
   )),
   ```

5. **Register both new rules in the dispatch lists**:
   - `section: $ => choice(..., $.ip_access_list_section)`
   - `section_header: $ => choice(..., $.ip_access_list_header)` — for `no ip access-list ...` (rare but valid negation; technically `no ip access-list ...` would also need `ip` in the negation path, which already works since `ip` is just an identifier)
   - `_ios_statement: $ => choice(..., $.access_list_statement)` — numbered ACLs at top level
   - `_command: $ => choice(..., $.access_list_statement)` — numbered ACLs inside other sections (rare)

### Disambiguation analysis

At line start of `ip access-list standard FOO`:

- Lexer state expects `identifier` (for command_line) OR `identifier` (for ip_access_list_header).
- Lexer produces `ip` as identifier (no promotion).
- After identifier, parser state expects:
  - For command_line: `value` (continue args) or `_nl` (end command_line).
  - For ip_access_list_header: `access_list_kw`.
- Both `value` matching "access-list" (11 chars) and `access_list_kw` (11 chars, prec 2) are valid.
- Tie on length, precedence wins → lexer produces `access_list_kw`.
- command_line's arg position accepts it via the alias → both branches survive → GLR fork.
- ip_access_list_header has `prec.right`, command_line has `prec.right`. Need to verify with `tree-sitter generate` whether this generates a conflict; if so, add `[ip_access_list_header]` to the grammar's `conflicts: $ => [...]` list, or bump ip_access_list_header to `prec.right(2)`.

At line start of `access-list 101 permit ip any any` (numbered):

- Lexer state expects `identifier` (for command_line) OR `access_list_kw` (for access_list_statement).
- Identifier matches "access" (6 chars); access_list_kw matches "access-list" (11 chars).
- Longest match wins → `access_list_kw`.
- command_line dies (expects identifier); access_list_statement wins.
- The old `command_line(access) + text(-list ...)` parse is replaced.

### Corpus tests (test/corpus/)

Add a new file `ip-access-list.txt` covering:

- Named standard ACL with `permit` body
- Named extended ACL with `permit tcp ... eq 443` body
- Named ACL with `deny` body
- Empty named ACL body
- Numbered ACL flat statement (replaces the old `command_line + text` parse)
- Negation (`no ip access-list standard FOO`)

Update `access-list.txt` corpus if any existing test asserted the old
`command_line(access) + text(-list ...)` shape — currently no test does
(the corpus tests `permit` / `deny` only).

### Symbols (internal/symbols/symbols.go)

1. **Add a sectionSpec entry** for `ip_access_list_section`:

   ```go
   {sectionKind: "ip_access_list_section", kind: KindACL,
    headerKind: "ip_access_list_header", nameField: "name"},
   ```

2. **Replace the numbered-ACL extraction**. The current code path
   matches `command_line(access)` + regex on the `text` sibling. After
   the grammar change, numbered ACLs are `access_list_statement`
   nodes. Extract directly:

   ```go
   // New case in extractACL:
   if n.Kind() == "access_list_statement" {
     args := namedArgs(n)
     if len(args) == 0 {
       return Symbol{}, false
     }
     sym := Symbol{
       Kind:      KindACL,
       Name:      textOf(args[0], content),  // the ACL number
       URI:       uri,
       Range:     nodeRange(n),
       NameRange: nodeRange(args[0]),
     }
     return sym, true
   }
   ```

   The `numberedACLRe` regex and the special-case `if leadingIdent == "access"`
   branch can be deleted entirely.

3. The existing named-ACL extraction (matching `command_line(ip) +
   arg(access-list) + ...`) should also be deleted because the new
   grammar routes those lines through `ip_access_list_header` instead.
   Verify by re-running the Phase 2/3/4 tests; the `TestExtract_ACLs`
   table will need its expected AST shape updated, but the symbol
   NAMES stay the same (FOO, BAR, 101, etc.).

### Risks and rollback

- **Lexer-commit trap**: the access_list_kw alias in `_cmd_arg` mirrors
  the proven Phase 1 pattern for the other six section keywords. If a
  new regression appears (e.g. some unrelated `access-list ...` line
  stops parsing), the fix is the same: verify the alias is in place
  and that `_cmd_arg` is the only arg-position rule.
- **GLR conflict on `ip` line start**: `tree-sitter generate` will
  report this as a conflict in `ip_access_list_header` vs `command_line`.
  Resolution is to add to the `conflicts` array. The existing
  `conflicts: $ => [[$.elif_statement], [$.router_header]]` pattern is
  the template.
- **Rollback**: revert the grammar commit and the symbols package
  commit. The Phase 2-7 features still work with the flat extraction
  (it is the current production code path).

### Implementation order

1. Grammar: add `access_list_kw`, `access_list_statement`,
   `ip_access_list_section`, `ip_access_list_header`. Add alias to
   `_cmd_arg`. Register in dispatch lists. Run `tree-sitter generate`
   and resolve any reported conflicts.
2. Corpus: add `test/corpus/ip-access-list.txt` with the cases above.
   Run `make test` in the TS repo — all 186 existing + new tests must
   pass.
3. Commit the grammar change in the TS repo, bumping the parser hash
   via `bindings/go/parser_hash.go` (chunt's Makefile does this
   automatically on the next build).
4. Symbols: add the new `sectionSpec` entry, replace the numbered-ACL
   regex path with the direct `access_list_statement` walk, delete the
   named-ACL `command_line(ip)` special case. Update `symbols_test.go`
   to assert the new AST shape; symbol NAMES stay the same.
5. Re-run `make test-lsp` and `chunt check` smoke tests. No behavior
   change should be visible to end users (the same diagnostics fire).
6. Commit the symbols change in chunt.

### Out of scope

- Nested DocumentSymbol children for ACE entries under their parent ACL
  (the grammar makes this possible but the LSP implementation keeps
  `Children: nil` for v1).
- A `policy_map_class_section` for the `class FOO` body inside a
  policy-map — same pattern, separate plan.
- Workspace / cross-file ACL resolution.
