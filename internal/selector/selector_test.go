package selector

import "testing"

func TestParseAndMatch(t *testing.T) {
	labels := map[string]string{
		"domain":   "code-review",
		"language": "go",
		"tier":     "prod",
	}
	cases := []struct {
		name    string
		expr    string
		want    bool
		wantErr bool
	}{
		{"equality match", "language=go", true, false},
		{"equality miss", "language=python", false, false},
		{"double equals", "language==go", true, false},
		{"not equal match", "language!=python", true, false},
		{"not equal miss", "language!=go", false, false},
		{"in match", "domain in (code-review, planning)", true, false},
		{"in miss", "domain in (planning, sre)", false, false},
		{"notin match", "domain notin (planning, sre)", true, false},
		{"notin miss", "domain notin (code-review)", false, false},
		{"exists", "tier", true, false},
		{"exists miss", "missing", false, false},
		{"not exists", "!missing", true, false},
		{"not exists miss", "!tier", false, false},
		{"anded all true", "language=go,domain in (code-review),tier", true, false},
		{"anded one false", "language=go,domain in (planning)", false, false},
		{"empty matches all", "", true, false},
		{"notin absent key matches", "absent notin (x,y)", true, false},
		{"malformed", "key in oops", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sel, err := Parse(tc.expr)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tc.expr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := sel.Matches(labels); got != tc.want {
				t.Fatalf("Matches(%q)=%v, want %v", tc.expr, got, tc.want)
			}
		})
	}
}

func TestEmptySelector(t *testing.T) {
	var s Selector
	if !s.Empty() {
		t.Fatal("zero selector should be empty")
	}
	if !s.Matches(map[string]string{"a": "b"}) {
		t.Fatal("empty selector should match everything")
	}
}
