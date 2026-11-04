package redact

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestJSONRedactionPreservesEscapesAndNestedValues(t *testing.T) {
	input := []byte(`{"data":{"prompt":"TOKEN=\"sk-abcdefghijklmnopqrstuvwxyz1234567890\"\nnext line","items":["password=quoted\\value"]}}`)
	out, err := (Redactor{}).JSON(input)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(out) || strings.Contains(string(out), "sk-abcdefghijklmnopqrstuvwxyz") || strings.Contains(string(out), "quoted\\\\value") {
		t.Fatalf("unsafe JSON redaction: %s", out)
	}
}

func TestJSONRedactionPreservesNumbersAndRejectsTrailingData(t *testing.T) {
	input := []byte(`{"exact":9007199254740993,"fraction":1.234567890123456789}`)
	out, err := (Redactor{}).JSON(input)
	if err != nil || string(out) != string(input) {
		t.Fatalf("numbers changed: %s, %v", out, err)
	}
	if _, err := (Redactor{}).JSON([]byte(`{} {}`)); err == nil {
		t.Fatal("accepted multiple JSON values")
	}
}

func TestJSONRedactionUsesCredentialFieldNames(t *testing.T) {
	out, err := (Redactor{}).JSON([]byte(`{"nested":{"password":"plain-value","api_key":"another-value"},"turn_id":"keep-me","keyframe_id":"retain-frame"}`))
	if err != nil || strings.Contains(string(out), "plain-value") || strings.Contains(string(out), "another-value") || !strings.Contains(string(out), "keep-me") {
		t.Fatalf("unsafe credential fields: %s, %v", out, err)
	}
	if !strings.Contains(string(out), "retain-frame") {
		t.Fatal("redacted a history identifier")
	}
	second, err := (Redactor{}).JSON(out)
	if err != nil || string(second) != string(out) {
		t.Fatalf("redaction must be idempotent: %s, %v", second, err)
	}
}
