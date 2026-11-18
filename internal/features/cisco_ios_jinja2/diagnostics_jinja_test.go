package cisco_ios_jinja2_test

import (
	"strings"
	"testing"
)

// Jinja false-positive gate (chunt-otg.21): computed names are unresolvable
// statically, guarded definitions satisfy references from any branch, and
// {% for %}-generated definitions register without duplicate false positives.

// Rule 1 acceptance: a template using {{ }} names produces ZERO undefined-*
// diagnostics.
func TestJinjaComputedReferenceNeverFlags(t *testing.T) {
	src := `interface Gi0/0
 ip access-group {{ acl_name }} in
 ip policy route-map {{ egress_policy }}
 switchport access vlan {{ voice_vlan }}
 service-policy input {{ qos_policy }}
!
`
	for _, d := range openDiags(t, src) {
		if strings.HasPrefix(d.Code, "undefined-") {
			t.Fatalf("computed-name reference produced %s %q", d.Code, d.Message)
		}
	}
}

// Rule 2 acceptance: a definition guarded by {% if %} satisfies a reference
// in another branch (and outside the guard entirely).
func TestJinjaGuardedDefinitionSatisfiesReference(t *testing.T) {
	src := `{% if role == "core" %}
ip access-list extended FOO
 permit ip any any
!
{% else %}
ip access-list extended BAR
 permit ip any any
!
{% endif %}
interface Gi0/0
 ip access-group FOO in
!
`
	for _, d := range openDiags(t, src) {
		if d.Code == "undefined-acl" {
			t.Fatalf("guarded definition did not satisfy reference: %q", d.Message)
		}
	}
}

// Rule 2, duplicate side: mutually exclusive if/else branches defining the
// same name must NOT surface duplicate-* warnings.
func TestJinjaGuardedDefinitionsNoDuplicateWarning(t *testing.T) {
	src := `{% if mode == "a" %}
ip access-list extended DUP
 permit ip any any
!
{% else %}
ip access-list extended DUP
 permit ip any any
!
{% endif %}
hostname {{ hostname }}
!
`
	for _, d := range openDiags(t, src) {
		if strings.HasPrefix(d.Code, "duplicate-") {
			t.Fatalf("guarded branches produced %s %q", d.Code, d.Message)
		}
	}
}

// Rule 3 acceptance: {% for %}-generated definitions register (extraction
// descends into Jinja blocks), and two for-loops emitting the same computed
// vlan name produce NO duplicate-vlan false positive.
func TestJinjaForGeneratedDefinitionsRegister(t *testing.T) {
	src := `{% for vlan_id in access_vlans %}
vlan {{ vlan_id }}
 name access
!
{% endfor %}
{% for vlan_id in voice_vlans %}
vlan {{ vlan_id }}
 name voice
!
{% endfor %}
interface Gi0/0
 switchport access vlan 10
!
`
	diags := openDiags(t, src)
	for _, d := range diags {
		if strings.HasPrefix(d.Code, "duplicate-") {
			t.Fatalf("computed-name definitions produced %s %q", d.Code, d.Message)
		}
	}
	// The literal vlan 10 reference is genuinely undefined: it must still be
	// flagged (the gate only suppresses COMPUTED names, not literal misses).
	if _, ok := findDiagByCode(diags, "undefined-vlan"); !ok {
		t.Fatalf("literal undefined vlan reference was not flagged")
	}
}

// Rule 4 acceptance, end to end: a definition written `{{- acl.name -}}`
// satisfies a reference written `{{ acl.name }}` (normalized comparison),
// via the symbol table the LSP Definition feature uses.
func TestJinjaWhitespaceControlNormalization(t *testing.T) {
	src := `{% for acl in acls %}
ip access-list extended {{- acl.name -}}
 permit ip any any
!
{% endfor %}
interface Gi0/0
 ip access-group {{ acl.name }} in
!
`
	for _, d := range openDiags(t, src) {
		if strings.HasPrefix(d.Code, "undefined-") || strings.HasPrefix(d.Code, "duplicate-") {
			t.Fatalf("whitespace-control names produced %s %q", d.Code, d.Message)
		}
	}
}

// Un-gated literal references must still be flagged inside Jinja blocks —
// the gate suppresses computed names, not misses.
func TestJinjaLiteralMissInsideGuardStillFlags(t *testing.T) {
	src := `{% if enable_acl %}
interface Gi0/0
 ip access-group TYPO-ACL in
!
{% endif %}
`
	diag, ok := findDiagByCode(openDiags(t, src), "undefined-acl")
	if !ok {
		t.Fatalf("literal undefined reference inside a guard was not flagged")
	}
	if !strings.Contains(diag.Message, "TYPO-ACL") {
		t.Fatalf("flagged wrong name: %q", diag.Message)
	}
}
