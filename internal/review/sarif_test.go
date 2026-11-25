package review

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// The official SARIF 2.1.0 JSON schemas, vendored unchanged:
//
//   - testdata/sarif-schema-2.1.0.oasis.json from
//     https://docs.oasis-open.org/sarif/sarif/v2.1.0/errata01/os/schemas/sarif-schema-2.1.0.json
//     (draft-04; sha256 c3b4bb2d6093897483348925aaa73af03b3e3f4bd4ca38cef26dcb4212a2682e)
//   - testdata/sarif-schema-2.1.0.schemastore.json from
//     https://json.schemastore.org/sarif-2.1.0.json, the $schema our logs name
//     (draft-07; sha256 c96eb2d311c37b0a38cbd18c52d79a68f778bbd2d831abed7412d9850740f785)
//
// Both were fetched on 2026-11-25. The validator below implements every
// keyword the two schemas use and fails on any other, so no constraint is
// silently skipped.
var sarifSchemas = []string{"sarif-schema-2.1.0.oasis.json", "sarif-schema-2.1.0.schemastore.json"}

type schemaValidator struct {
	root map[string]any
}

var annotationKeywords = map[string]bool{"$schema": true, "$id": true, "id": true, "title": true, "description": true, "default": true, "definitions": true}

func (v schemaValidator) validate(schema any, value any, at string) []string {
	s, ok := schema.(map[string]any)
	if !ok {
		if b, ok := schema.(bool); ok && !b {
			return []string{at + ": no value is allowed"}
		}
		return nil
	}
	var errs []string
	fail := func(format string, args ...any) { errs = append(errs, at+": "+fmt.Sprintf(format, args...)) }
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, keyword := range keys {
		arg := s[keyword]
		switch keyword {
		case "$ref":
			ref := arg.(string)
			name, ok := strings.CutPrefix(ref, "#/definitions/")
			if !ok {
				fail("unsupported $ref %s", ref)
				continue
			}
			target, ok := v.root["definitions"].(map[string]any)[name]
			if !ok {
				fail("unknown $ref %s", ref)
				continue
			}
			errs = append(errs, v.validate(target, value, at)...)
		case "type":
			var types []string
			if t, ok := arg.(string); ok {
				types = []string{t}
			} else {
				for _, t := range arg.([]any) {
					types = append(types, t.(string))
				}
			}
			matched := false
			for _, t := range types {
				matched = matched || hasType(value, t)
			}
			if !matched {
				fail("want type %v, got %T", types, value)
			}
		case "properties":
			object, ok := value.(map[string]any)
			if !ok {
				continue
			}
			for name, sub := range arg.(map[string]any) {
				if item, ok := object[name]; ok {
					errs = append(errs, v.validate(sub, item, at+"."+name)...)
				}
			}
		case "additionalProperties":
			object, ok := value.(map[string]any)
			if !ok {
				continue
			}
			known, _ := s["properties"].(map[string]any)
			for name, item := range object {
				if _, ok := known[name]; ok {
					continue
				}
				if b, ok := arg.(bool); ok {
					if !b {
						fail("property %q is not allowed", name)
					}
					continue
				}
				errs = append(errs, v.validate(arg, item, at+"."+name)...)
			}
		case "required":
			object, ok := value.(map[string]any)
			if !ok {
				continue
			}
			for _, name := range arg.([]any) {
				if _, ok := object[name.(string)]; !ok {
					fail("missing required property %q", name)
				}
			}
		case "items":
			array, ok := value.([]any)
			if !ok {
				continue
			}
			for i, item := range array {
				errs = append(errs, v.validate(arg, item, fmt.Sprintf("%s[%d]", at, i))...)
			}
		case "minItems":
			if array, ok := value.([]any); ok && float64(len(array)) < arg.(float64) {
				fail("fewer than %v items", arg)
			}
		case "uniqueItems":
			array, ok := value.([]any)
			if !ok || arg != true {
				continue
			}
			seen := map[string]bool{}
			for _, item := range array {
				key, _ := json.Marshal(item)
				if seen[string(key)] {
					fail("duplicate item %s", key)
				}
				seen[string(key)] = true
			}
		case "minimum":
			if n, ok := value.(float64); ok && n < arg.(float64) {
				fail("%v is below the minimum %v", n, arg)
			}
		case "maximum":
			if n, ok := value.(float64); ok && n > arg.(float64) {
				fail("%v is above the maximum %v", n, arg)
			}
		case "enum":
			found := false
			for _, option := range arg.([]any) {
				found = found || equalJSON(option, value)
			}
			if !found {
				fail("%v is not one of %v", value, arg)
			}
		case "const":
			if !equalJSON(arg, value) {
				fail("%v is not %v", value, arg)
			}
		case "pattern":
			if text, ok := value.(string); ok && !regexp.MustCompile(arg.(string)).MatchString(text) {
				fail("%q does not match %s", text, arg)
			}
		case "format":
			text, ok := value.(string)
			if !ok {
				continue
			}
			switch arg {
			case "uri":
				if u, err := url.Parse(text); err != nil || u.Scheme == "" {
					fail("%q is not an absolute URI", text)
				}
			case "uri-reference":
				if _, err := url.Parse(text); err != nil || strings.ContainsAny(text, " \\") {
					fail("%q is not a URI reference", text)
				}
			case "date-time":
				if _, err := time.Parse(time.RFC3339, text); err != nil {
					fail("%q is not a date-time", text)
				}
			default:
				fail("unsupported format %v", arg)
			}
		case "anyOf", "oneOf":
			matches := 0
			for _, option := range arg.([]any) {
				if len(v.validate(option, value, at)) == 0 {
					matches++
				}
			}
			if matches == 0 || (keyword == "oneOf" && matches > 1) {
				fail("%s matched %d schemas", keyword, matches)
			}
		default:
			if !annotationKeywords[keyword] {
				fail("unsupported schema keyword %q", keyword)
			}
		}
	}
	return errs
}

func hasType(value any, t string) bool {
	switch t {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "number":
		_, ok := value.(float64)
		return ok
	case "integer":
		n, ok := value.(float64)
		return ok && n == math.Trunc(n)
	case "null":
		return value == nil
	}
	return false
}

func equalJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

func loadSchema(t *testing.T, name string) schemaValidator {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	return schemaValidator{root: root}
}

func validateSARIF(t *testing.T, log []byte) {
	t.Helper()
	var value any
	if err := json.Unmarshal(log, &value); err != nil {
		t.Fatalf("SARIF is not JSON: %v", err)
	}
	for _, name := range sarifSchemas {
		v := loadSchema(t, name)
		if errs := v.validate(v.root, value, "$"); len(errs) > 0 {
			t.Fatalf("SARIF fails %s:\n%s\n%s", name, strings.Join(errs, "\n"), log)
		}
	}
}

func sampleReport() Report {
	return Report{
		Reviewers: []string{"claude-code", "codex"},
		Judge:     "codex",
		Source:    "judge",
		Files:     []FileDiff{{Path: "dir with space/a.go", Hunks: []Hunk{{NewStart: 1, NewCount: 2, Added: []int{1, 2}}}}, {Path: "b.png", Binary: true}},
		Findings: []Finding{
			{File: "dir with space/a.go", StartLine: 3, EndLine: 5, Severity: Critical, Title: "SQL built from input", Detail: "Uses fmt.Sprintf.\nSee @octocat <script>", Reviewers: []string{"claude-code", "codex"}, Agreement: 2},
			{File: "b.go", StartLine: 7, EndLine: 7, Severity: Medium, Title: "Unchecked error", Reviewers: []string{"codex"}, Agreement: 1},
			{File: "c%d.go", Severity: Low, Title: "Name", Reviewers: []string{"codex"}, Agreement: 1},
			{Severity: Info, Title: "General note | with pipe", Agreement: 0},
		},
		Outcomes: []Outcome{
			{Agent: "claude-code", Status: "completed"},
			{Agent: "codex", Status: "timed_out"},
			{Agent: "codex", Status: "completed", Raw: "prose ``` answer"},
		},
	}
}

func TestSARIFValidatesAgainstTheOfficialSchemas(t *testing.T) {
	for name, r := range map[string]Report{
		"full":  sampleReport(),
		"empty": {Reviewers: []string{"codex"}, Source: "merged"},
	} {
		t.Run(name, func(t *testing.T) {
			log, err := SARIF(r)
			if err != nil {
				t.Fatal(err)
			}
			validateSARIF(t, log)
		})
	}
}

func TestSARIFContent(t *testing.T) {
	log, err := SARIF(sampleReport())
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Version string `json:"version"`
		Runs    []struct {
			Invocations []struct {
				ExecutionSuccessful bool `json:"executionSuccessful"`
			} `json:"invocations"`
			Results []struct {
				RuleID    string `json:"ruleId"`
				Level     string `json:"level"`
				Locations []struct {
					PhysicalLocation struct {
						ArtifactLocation struct {
							URI string `json:"uri"`
						} `json:"artifactLocation"`
						Region *struct {
							StartLine int `json:"startLine"`
							EndLine   int `json:"endLine"`
						} `json:"region"`
					} `json:"physicalLocation"`
				} `json:"locations"`
				Properties struct {
					Agreement int      `json:"agreement"`
					Reviewers []string `json:"reviewers"`
				} `json:"properties"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(log, &doc); err != nil {
		t.Fatal(err)
	}
	results := doc.Runs[0].Results
	if doc.Version != "2.1.0" || len(results) != 5 || doc.Runs[0].Invocations[0].ExecutionSuccessful {
		t.Fatalf("unexpected log: %s", log)
	}
	first := results[0]
	if first.Level != "error" || first.Locations[0].PhysicalLocation.ArtifactLocation.URI != "dir%20with%20space/a.go" ||
		first.Locations[0].PhysicalLocation.Region.StartLine != 3 || first.Locations[0].PhysicalLocation.Region.EndLine != 5 || first.Properties.Agreement != 2 {
		t.Fatalf("first result: %+v", first)
	}
	if results[1].Level != "warning" || results[2].Level != "note" || results[2].Locations[0].PhysicalLocation.ArtifactLocation.URI != "c%25d.go" || results[2].Locations[0].PhysicalLocation.Region != nil {
		t.Fatalf("levels or file-only location: %+v", results)
	}
	if len(results[3].Locations) != 0 || results[4].RuleID != RuleUnstructured {
		t.Fatalf("general or unstructured result: %+v", results)
	}
}

// The validator must reject what the schema forbids, or the schema test
// proves nothing.
func TestSchemaValidatorRejectsInvalidLogs(t *testing.T) {
	valid, err := SARIF(sampleReport())
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(map[string]any){
		"wrong version":    func(d map[string]any) { d["version"] = "2.0.0" },
		"missing runs":     func(d map[string]any) { delete(d, "runs") },
		"unknown property": func(d map[string]any) { d["extra"] = true },
		"bad level":        func(d map[string]any) { result(d)["level"] = "fatal" },
		"zero start line":  func(d map[string]any) { region(d)["startLine"] = 0 },
		"message without text": func(d map[string]any) {
			result(d)["message"] = map[string]any{}
		},
		"bad uri": func(d map[string]any) {
			result(d)["locations"].([]any)[0].(map[string]any)["physicalLocation"].(map[string]any)["artifactLocation"].(map[string]any)["uri"] = "a b.go"
		},
	} {
		t.Run(name, func(t *testing.T) {
			var doc map[string]any
			if err := json.Unmarshal(valid, &doc); err != nil {
				t.Fatal(err)
			}
			mutate(doc)
			for _, schema := range sarifSchemas {
				v := loadSchema(t, schema)
				if errs := v.validate(v.root, doc, "$"); len(errs) == 0 {
					t.Fatalf("%s accepted an invalid log", schema)
				}
			}
		})
	}
}

func result(d map[string]any) map[string]any {
	return d["runs"].([]any)[0].(map[string]any)["results"].([]any)[0].(map[string]any)
}

func region(d map[string]any) map[string]any {
	return result(d)["locations"].([]any)[0].(map[string]any)["physicalLocation"].(map[string]any)["region"].(map[string]any)
}
