package adapters

import (
	"strings"
	"testing"
)

func TestPoolsideSetupVersionParsing(t *testing.T) {
	for _, tt := range []struct {
		version string
		old     bool
	}{
		{"1.0.14", true}, {"v1.0.16-rc.1", true}, {"1.0.16", false},
		{"pool 1.1.0", false}, {"2.0.0", false}, {"unknown", false},
	} {
		t.Run(tt.version, func(t *testing.T) {
			if got := strings.Contains(poolsideVersionNotice(tt.version), "too old"); got != tt.old {
				t.Fatalf("version %s: got old=%t", tt.version, got)
			}
		})
	}
}
