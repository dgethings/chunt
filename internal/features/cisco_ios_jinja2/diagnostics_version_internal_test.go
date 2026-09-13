package cisco_ios_jinja2

import "testing"

// TestCompareVersions covers the conservative version comparator: numeric
// dot-versions compare component-wise; differing lengths resolve on the
// non-zero tail; any non-integer component makes the pair incomparable so the
// diagnostic pass refuses to flag on heuristic values like "3.9S"
// (chunt-y9d).
func TestCompareVersions(t *testing.T) {
	cases := []struct {
		name       string
		a, b       string
		want       int
		comparable bool
	}{
		{"equal", "17.3", "17.3", 0, true},
		{"a < b (major)", "12.2", "17.3", -1, true},
		{"a > b (major)", "26.1.0", "17.3", 1, true},
		{"a < b (minor)", "17.2", "17.3", -1, true},
		{"a > b (minor)", "17.4", "17.3", 1, true},
		{"trailing zero is equal", "17.3", "17.3.0", 0, true},
		{"trailing nonzero is greater", "17.3.1", "17.3", 1, true},
		{"a > b (patch)", "17.3.2", "17.3.1", 1, true},
		// Heuristic / non-numeric components must be refused, not guessed.
		{"non-numeric suffix incomparable", "3.9S", "17.3", 0, false},
		{"parenthesised train incomparable", "15.7(3)M", "17.3", 0, false},
		{"empty component incomparable", "17.", "17.3", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := compareVersions(tc.a, tc.b)
			if ok != tc.comparable {
				t.Fatalf("comparable = %v, want %v", ok, tc.comparable)
			}
			if ok && got != tc.want {
				t.Errorf("cmp = %d, want %d", got, tc.want)
			}
		})
	}
}
