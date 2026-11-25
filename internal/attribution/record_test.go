package attribution

import "testing"

// A record written before a secret exclusion was added can name the file.
// The rest of the commit's attribution must survive; the secret entry and
// structurally unsafe records must not.
func TestDecodeRecordDropsSinceExcludedFiles(t *testing.T) {
	const origins = `"origins":[{"kind":"turn","turn":"T1","agent":"codex","confidence":"exact"}]`
	record := decodeRecord(`{"v":1,"files":{"app.go":[[1,2,0]],".npmrc":[[1,1,0]]},` + origins + `}`)
	if record == nil {
		t.Fatal("record naming .npmrc was discarded")
	}
	if _, ok := record.Files[".npmrc"]; ok {
		t.Fatal("record kept the excluded .npmrc")
	}
	if _, ok := record.lookup("app.go", 1); !ok {
		t.Fatal("app.go attribution lost")
	}
	for _, path := range []string{"../escape.go", ".git/hooks/pre-commit", "sub/.GIT/config"} {
		if decodeRecord(`{"v":1,"files":{"`+path+`":[[1,1,0]]},`+origins+`}`) != nil {
			t.Fatalf("record naming %q accepted", path)
		}
	}
}
