package cli

import (
	"reflect"
	"testing"
)

func TestCompanionPluginRepositoryRouting(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want []string
		repo string
	}{
		{[]string{"--repo", "/project with spaces", "remember", "note"}, []string{"remember", "note"}, "/project with spaces"},
		{[]string{"context", "--repo=/project", "--path", "main.go"}, []string{"context", "--path", "main.go"}, "/project"},
		{[]string{"remember", "--", "--repo", "literal"}, []string{"remember", "--", "--repo", "literal"}, "inherited"},
	} {
		got, repo, err := pluginRepoArgs(tc.args, "inherited")
		if err != nil || repo != tc.repo || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%v -> %v, %q, %v", tc.args, got, repo, err)
		}
	}
	if _, _, err := pluginRepoArgs([]string{"--repo"}, ""); err == nil {
		t.Fatal("missing repository flag value accepted")
	}
}
