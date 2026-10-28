package id

import "testing"

func TestNewMonotonic(t *testing.T) {
	previous := ""
	for i := 0; i < 10000; i++ {
		current, err := New()
		if err != nil {
			t.Fatal(err)
		}
		if len(current) != 26 || current <= previous {
			t.Fatalf("ULID order failed at %d: %q <= %q", i, current, previous)
		}
		if current[0] > '7' {
			t.Fatalf("invalid ULID leading character: %q", current)
		}
		previous = current
	}
}
