package engine

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestChangedHunksLargeFileWithLocalEdit(t *testing.T) {
	old := make([]string, 3000)
	for i := range old {
		old[i] = fmt.Sprintf("unique-line-%04d", i)
	}
	before := strings.Join(old, "\n") + "\n"
	for _, tc := range []struct {
		name string
		new  []string
		want []hunk
	}{
		{"insertion", slices.Insert(slices.Clone(old), 100, "inserted-line"), []hunk{{101, 101}}},
		{"deletion", slices.Delete(slices.Clone(old), 100, 101), []hunk{{0, 0}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := changedHunks(before, strings.Join(tc.new, "\n")+"\n")
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("changed hunks = %v, want %v", got, tc.want)
			}
		})
	}
}
