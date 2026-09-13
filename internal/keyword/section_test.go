package keyword_test

import (
	"reflect"
	"sort"
	"testing"

	"github.com/dgethings/chunt/internal/keyword"
)

func TestBuildSectionTree_BasicHierarchy(t *testing.T) {
	sections := []string{
		"config-if",
		"config-router",
		"config-router-af",
		"config-router-af-topology",
		"config-pmap",
		"config-pmap-c",
		"config-archive-log-config",
		"config-archive",
	}
	tree := keyword.BuildSectionTree(sections)

	cases := map[string]string{
		"config-router-af-topology": "config-router-af",
		"config-router-af":          "config-router",
		"config-router":             "config",
		"config-pmap-c":             "config-pmap",
		"config-pmap":               "config",
		"config-archive-log-config": "config-archive",
		"config-archive":            "config",
		"config-if":                 "config",
		"config":                    "",
	}
	for section, want := range cases {
		if got := tree.Parent(section); got != want {
			t.Errorf("Parent(%q) = %q, want %q", section, got, want)
		}
	}
}

func TestBuildSectionTree_MultiLevelStrip(t *testing.T) {
	sections := []string{
		"config-archive-log-config",
		"config-archive",
	}
	tree := keyword.BuildSectionTree(sections)

	if got := tree.Parent("config-archive-log-config"); got != "config-archive" {
		t.Errorf("Parent(config-archive-log-config) = %q, want config-archive", got)
	}
	if got := tree.Parent("config-archive"); got != "config" {
		t.Errorf("Parent(config-archive) = %q, want config", got)
	}
}

func TestBuildSectionTree_RootParent(t *testing.T) {
	tree := keyword.BuildSectionTree(nil)
	if got := tree.Parent("config"); got != "" {
		t.Errorf("Parent(config) = %q, want empty string for root", got)
	}
	// Parent is a node lookup; an unknown section has no node and returns "".
	// Walking up to a fallback ancestor is the job of NearestKnown, not Parent.
	if got := tree.Parent("config-if"); got != "" {
		t.Errorf("Parent(config-if) = %q, want empty (section not in tree)", got)
	}
}

func TestBuildSectionTree_Children(t *testing.T) {
	sections := []string{
		"config-router",
		"config-router-af",
		"config-if",
	}
	tree := keyword.BuildSectionTree(sections)

	routerChildren := tree.Children("config-router")
	if !contains(routerChildren, "config-router-af") {
		t.Errorf("Children(config-router) = %v, want to contain config-router-af", routerChildren)
	}
	if len(routerChildren) != 1 {
		t.Errorf("Children(config-router) = %v, want exactly 1 child", routerChildren)
	}

	configChildren := tree.Children("config")
	sortedConfig := append([]string{}, configChildren...)
	sort.Strings(sortedConfig)
	wantConfig := []string{"config-if", "config-router"}
	if !reflect.DeepEqual(sortedConfig, wantConfig) {
		t.Errorf("Children(config) = %v, want %v", sortedConfig, wantConfig)
	}

	if got := tree.Children("config-nonexistent"); got != nil {
		t.Errorf("Children(config-nonexistent) = %v, want nil", got)
	}
}

func TestAncestors(t *testing.T) {
	sections := []string{
		"config-router",
		"config-router-af",
		"config-router-af-topology",
	}
	tree := keyword.BuildSectionTree(sections)

	got := tree.Ancestors("config-router-af-topology")
	want := []string{"config-router-af", "config-router", "config"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Ancestors(config-router-af-topology) = %v, want %v", got, want)
	}

	if got := tree.Ancestors("config"); len(got) != 0 {
		t.Errorf("Ancestors(config) = %v, want empty (root has no ancestors)", got)
	}

	if got := tree.Ancestors("config-nonexistent"); got != nil {
		t.Errorf("Ancestors(config-nonexistent) = %v, want nil", got)
	}
}

func TestIsAncestor(t *testing.T) {
	sections := []string{
		"config-router",
		"config-router-af",
		"config-if",
	}
	tree := keyword.BuildSectionTree(sections)

	cases := []struct {
		name     string
		ancestor string
		section  string
		want     bool
	}{
		{"config is ancestor of af", "config", "config-router-af", true},
		{"router is ancestor of af", "config-router", "config-router-af", true},
		{"sibling branch not ancestor", "config-if", "config-router-af", false},
		{"self is ancestor", "config-router-af", "config-router-af", true},
		{"af is not ancestor of router", "config-router-af", "config-router", false},
		{"config is ancestor of itself", "config", "config", true},
		{"unknown ancestor", "config-bogus", "config-router-af", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tree.IsAncestor(tc.ancestor, tc.section); got != tc.want {
				t.Errorf("IsAncestor(%q, %q) = %v, want %v", tc.ancestor, tc.section, got, tc.want)
			}
		})
	}
}

func TestNearestKnown(t *testing.T) {
	sections := []string{
		"config-router",
		"config-router-af",
		"config-router-af-topology",
		"config-if",
	}
	tree := keyword.BuildSectionTree(sections)
	known := map[string]bool{
		"config-router": true,
		"config":        true,
	}

	cases := map[string]string{
		"config-router-af-topology": "config-router",
		"config-router-af":          "config-router",
		"config-router":             "config-router",
		"config-if":                 "config",
		"config":                    "config",
		"config-totally-unknown":    "config",
	}
	for section, want := range cases {
		if got := tree.NearestKnown(section, known); got != want {
			t.Errorf("NearestKnown(%q) = %q, want %q", section, got, want)
		}
	}
}

func TestNearestKnown_WithRealKeywordSections(t *testing.T) {
	// Simulate the completion fallback scenario: a section detected by the
	// grammar that has no keywords, falling back to the nearest ancestor.
	sections := []string{"config", "config-if", "config-router", "config-router-af", "config-pmap", "config-pmap-c"}
	tree := keyword.BuildSectionTree(sections)

	// config-router-af has keywords — no fallback needed
	known := map[string]bool{"config-router-af": true, "config-router": true, "config": true, "config-if": true}
	if got := tree.NearestKnown("config-router-af", known); got != "config-router-af" {
		t.Errorf("NearestKnown(config-router-af) = %q, want config-router-af", got)
	}

	// config-pmap-c has NO keywords — should fall back to config-pmap
	known2 := map[string]bool{"config-pmap": true, "config": true}
	if got := tree.NearestKnown("config-pmap-c", known2); got != "config-pmap" {
		t.Errorf("NearestKnown(config-pmap-c) = %q, want config-pmap", got)
	}
}

func TestBuildSectionTree_RealDataFixture(t *testing.T) {
	sections := []string{
		"config",
		"config-if",
		"config-if-atm-range",
		"config-if-atm-range-pvc",
		"config-line",
		"config-router",
		"config-router-af",
		"config-router-af-interface",
		"config-router-af-topology",
		"config-router-lisp",
		"config-router-lisp-site",
		"config-router-lisp-eid-table",
		"config-router-lisp-eid-table-dynamic-eid",
		"config-pmap",
		"config-pmap-c",
		"config-pmap-c-metric",
		"config-archive",
		"config-archive-log-config",
		"config-dhcp",
		"config-dhcp-pool-class",
		"config-dhcp-subnet-secondary",
		"config-control-policymap",
		"config-control-policymap-class-control",
		"config-vpdn",
		"config-vpdn-acc-in",
		"config-ip-sla",
		"config-ip-sla-ethernet-monitor",
		"config-ip-sla-ethernet-jitter",
		"config-domain-vrf",
		"config-domain-vrf-mc",
	}

	tree := keyword.BuildSectionTree(sections)

	if got := tree.Parent("config-router-af-topology"); got != "config-router-af" {
		t.Errorf("Parent(config-router-af-topology) = %q, want config-router-af", got)
	}
	if got := tree.Parent("config-router-lisp-eid-table-dynamic-eid"); got != "config-router-lisp-eid-table" {
		t.Errorf("Parent(config-router-lisp-eid-table-dynamic-eid) = %q, want config-router-lisp-eid-table", got)
	}
	if got := tree.Parent("config-router-lisp-site"); got != "config-router-lisp" {
		t.Errorf("Parent(config-router-lisp-site) = %q, want config-router-lisp", got)
	}
	if got := tree.Parent("config-archive-log-config"); got != "config-archive" {
		t.Errorf("Parent(config-archive-log-config) = %q, want config-archive (skips missing -log)", got)
	}
	if got := tree.Parent("config-dhcp-subnet-secondary"); got != "config-dhcp" {
		t.Errorf("Parent(config-dhcp-subnet-secondary) = %q, want config-dhcp (no -pool in set)", got)
	}
	if got := tree.Parent("config-control-policymap-class-control"); got != "config-control-policymap" {
		t.Errorf("Parent(config-control-policymap-class-control) = %q, want config-control-policymap", got)
	}
	if got := tree.Parent("config-if-atm-range-pvc"); got != "config-if-atm-range" {
		t.Errorf("Parent(config-if-atm-range-pvc) = %q, want config-if-atm-range", got)
	}
	if got := tree.Parent("config-ip-sla-ethernet-jitter"); got != "config-ip-sla" {
		t.Errorf("Parent(config-ip-sla-ethernet-jitter) = %q, want config-ip-sla", got)
	}
	if got := tree.Parent("config-domain-vrf-mc"); got != "config-domain-vrf" {
		t.Errorf("Parent(config-domain-vrf-mc) = %q, want config-domain-vrf", got)
	}

	ancestors := tree.Ancestors("config-router-lisp-eid-table-dynamic-eid")
	want := []string{"config-router-lisp-eid-table", "config-router-lisp", "config-router", "config"}
	if !reflect.DeepEqual(ancestors, want) {
		t.Errorf("Ancestors(config-router-lisp-eid-table-dynamic-eid) = %v, want %v", ancestors, want)
	}

	if !tree.IsAncestor("config", "config-if-atm-range-pvc") {
		t.Error("IsAncestor(config, config-if-atm-range-pvc) = false, want true")
	}
	if !tree.IsAncestor("config-if-atm-range", "config-if-atm-range-pvc") {
		t.Error("IsAncestor(config-if-atm-range, config-if-atm-range-pvc) = false, want true")
	}
}

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
