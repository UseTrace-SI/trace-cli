package cli

import "testing"

func TestHostedStackVersionComparison(t *testing.T) {
	for _, tc := range []struct {
		latest, current string
		want            bool
	}{
		{"1.0.0", "1.1.0-solo.1", false}, {"v1.1.0-solo.2", "1.1.0-solo.1", true}, {"1.1.0", "1.1.0-solo.2", true}, {"1.1.0-solo.2", "1.1.0", false}, {"1.1.0-solo.10", "1.1.0-solo.9", true}, {"bad", "1.1.0", false},
	} {
		if got := newerVersion(tc.latest, tc.current); got != tc.want {
			t.Errorf("%s > %s = %v", tc.latest, tc.current, got)
		}
	}
}
