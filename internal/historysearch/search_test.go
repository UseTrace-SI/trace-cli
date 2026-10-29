package historysearch

import "testing"

func TestTerms(t *testing.T) {
	for _, tc := range []struct {
		query, want string
		invalid     bool
	}{
		{"sign-in", `("sign-in")`, false},
		{"deadlock|panic sqlite", `("deadlock" OR "panic") AND ("sqlite")`, false},
		{"  café  API  ", `("café") AND ("API")`, false},
		{"", "", false},
		{"|", "", true},
	} {
		t.Run(tc.query, func(t *testing.T) {
			got, err := terms(tc.query)
			if (err != nil) != tc.invalid || got != tc.want {
				t.Fatalf("terms(%q) = %q, %v; want %q, invalid=%t", tc.query, got, err, tc.want, tc.invalid)
			}
		})
	}
}
