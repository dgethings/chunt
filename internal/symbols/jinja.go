// Package symbols — Jinja-aware definition/reference resolution
// (chunt-otg.21).
//
// Real .ios.j2 templates compute object names at render time (`ip access-group
// {{ acl_name }} in`) and guard definitions with `{% if %}` branches. Static
// analysis cannot resolve computed names and cannot prove which branches
// render, so the diagnostics passes must gate on four rules:
//
//  1. A name containing a Jinja expression/statement tag is UNRESOLVABLE —
//     never emit undefined-* for it (HasJinjaExpression).
//  2. Definitions guarded by {% if %} still satisfy references from any
//     branch; the symbol table already indexes them, and the Guarded flag
//     lets the duplicate-definition pass skip them (mutually exclusive
//     branches legitimately repeat a definition).
//  3. {% for %}-generated definitions (name built from the loop variable)
//     register as definitions — extraction already descends into Jinja
//     blocks; the computed name is normalized for consistent comparison.
//  4. Whitespace-control and whitespace inside a name token are stripped
//     before comparison so `{{ name }}`, `{{name}}`, and `{{- name -}}`
//     compare equal (NormalizeJinjaName).
//
// The implementations of these rules live in symbols.go alongside the
// extraction they post-process (finishSymbol / finishReference).
package symbols
