package symbols_test

import (
	"testing"

	"github.com/dgethings/chunt/internal/symbols"
)

// Jinja-aware definition/reference resolution (chunt-otg.21).

func TestHasJinjaExpression(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"literal", "ACL-V4", false},
		{"number", "99", false},
		{"empty", "", false},
		{"expression", "{{ acl_name }}", true},
		{"expression no spaces", "{{acl_name}}", true},
		{"whitespace control", "{{- acl_name -}}", true},
		{"statement tag", "{% if x %}", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := symbols.HasJinjaExpression(tc.in); got != tc.want {
				t.Fatalf("HasJinjaExpression(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizeJinjaName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"literal unchanged", "ACL-V4", "ACL-V4"},
		{"empty unchanged", "", ""},
		{"expression", "{{ acl_name }}", "{{acl_name}}"},
		{"expression no spaces already", "{{acl_name}}", "{{acl_name}}"},
		{"whitespace control", "{{- acl_name -}}", "{{acl_name}}"},
		{"inner whitespace collapsed", "{{ intf.name }}", "{{intf.name}}"},
		{"interior dash kept", "{{ a-b }}", "{{a-b}}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := symbols.NormalizeJinjaName(tc.in); got != tc.want {
				t.Fatalf("NormalizeJinjaName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
	// The three render-time spellings of one expression must collapse to the
	// same normalized key (rule 4).
	a := symbols.NormalizeJinjaName("{{ vlan_id }}")
	b := symbols.NormalizeJinjaName("{{vlan_id}}")
	c := symbols.NormalizeJinjaName("{{- vlan_id -}}")
	if a != b || b != c {
		t.Fatalf("spellings do not converge: %q %q %q", a, b, c)
	}
}

func TestExtractGuardedDefinitions(t *testing.T) {
	src := `ip access-list extended TOP
 permit ip any any
!
{% if role == "core" %}
ip access-list extended CORE
 permit ip any any
!
{% else %}
ip access-list extended BRANCH
 permit ip any any
!
{% endif %}
{% for vlan_id in vlans %}
vlan {{ vlan_id }}
 name guest
!
{% endfor %}
`
	root, content := parseRoot(t, src)
	syms := symbols.Extract("file:///t.ios.j2", root, content)
	guarded := map[string]bool{}
	for _, s := range syms {
		guarded[string(s.Kind)+" "+s.Name] = s.Guarded
	}
	want := map[string]bool{
		"acl TOP":            false,
		"acl CORE":           true,
		"acl BRANCH":         true,
		"vlan {{vlan_id}}":   true,
	}
	if len(guarded) != len(want) {
		t.Fatalf("got %v, want keys %v", guarded, want)
	}
	for k, g := range want {
		got, ok := guarded[k]
		if !ok {
			t.Fatalf("missing symbol %q (have %v)", k, guarded)
		}
		if got != g {
			t.Fatalf("symbol %q Guarded = %v, want %v", k, got, g)
		}
	}
}

// Rule 3 acceptance: a definition inside {% for %} registers, and its
// normalized computed name matches a normalized computed reference so
// downstream Lookup-family queries are consistent.
func TestExtractComputedNameNormalization(t *testing.T) {
	src := `{% for acl in acls %}
ip access-list extended {{ acl.name }}
 permit ip any any
!
{% endfor %}
interface Gi0/0
 ip access-group {{ acl.name }} in
!
`
	root, content := parseRoot(t, src)
	syms := symbols.Extract("file:///t.ios.j2", root, content)
	refs := symbols.ExtractReferences("file:///t.ios.j2", root, content)
	var acl *symbols.Symbol
	for i := range syms {
		if syms[i].Kind == symbols.KindACL {
			acl = &syms[i]
		}
	}
	if acl == nil {
		t.Fatalf("no ACL symbol in %v", syms)
	}
	if acl.Name != "{{acl.name}}" {
		t.Fatalf("definition name = %q, want normalized {{acl.name}}", acl.Name)
	}
	if len(refs) != 1 || refs[0].Name != "{{acl.name}}" {
		t.Fatalf("references = %v, want one normalized computed ref", refs)
	}
	tbl := symbols.NewTable()
	tbl.Index("file:///t.ios.j2", root, content)
	t.Cleanup(func() { tbl.Clear("file:///t.ios.j2") })
	if got := tbl.Lookup("file:///t.ios.j2", symbols.KindACL, refs[0].Name); len(got) != 1 {
		t.Fatalf("computed ref does not resolve to computed def: %v", got)
	}
}
