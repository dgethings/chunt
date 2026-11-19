package cisco_ios_jinja2_test

import (
	"strings"
	"testing"
)

// Community-list and as-path undefined-reference diagnostics (chunt-otg.12).

func TestUndefinedCommunityAndAsPath(t *testing.T) {
	src := `route-map RM permit 10
 match community MISSING-COMM
 match community 99 exact-match
 match as-path MISSING-AP 40
!
ip community-list standard OK-COMM permit 65000:100
ip as-path access-list OK-AP permit ^65000$
!
`
	var codes []string
	for _, d := range openDiags(t, src) {
		switch d.Code {
		case "undefined-community-list", "undefined-as-path-acl":
			codes = append(codes, d.Code+": "+d.Message)
		}
	}
	if len(codes) != 4 {
		t.Fatalf("flagged %v, want four diagnostics (MISSING-COMM, 99, MISSING-AP, 40)", codes)
	}
	joined := strings.Join(codes, " | ")
	for _, want := range []string{"MISSING-COMM", "99", "MISSING-AP", "40"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %v", want, codes)
		}
	}
	for _, def := range []string{"OK-COMM", "OK-AP"} {
		if strings.Contains(joined, def) {
			t.Fatalf("defined object flagged: %v", codes)
		}
	}
}
