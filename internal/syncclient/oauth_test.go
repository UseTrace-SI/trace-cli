package syncclient

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestDeviceFlow(t *testing.T) {
	requests := 0
	o := OAuth{Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.Header.Get("Accept") != "application/json" {
			t.Fatal("JSON response not requested")
		}
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		switch r.URL.Path {
		case "/login/device/code":
			if !strings.Contains(string(b), "client_id=client123") {
				t.Fatal("missing client ID")
			}
			return response(`{"device_code":"device","user_code":"ABCD-EFGH","verification_uri":"https://github.com/login/device","expires_in":10,"interval":1}`), nil
		case "/login/oauth/access_token":
			if !strings.Contains(string(b), "grant_type=urn%3Aietf%3Aparams%3Aoauth%3Agrant-type%3Adevice_code") {
				t.Fatal("wrong grant type")
			}
			return response(`{"access_token":"test-token"}`), nil
		default:
			t.Fatalf("unexpected OAuth path %q", r.URL.Path)
			return nil, nil
		}
	})}}
	code, err := o.Start(context.Background(), "client123")
	if err != nil || code.UserCode != "ABCD-EFGH" {
		t.Fatalf("device code: %+v, %v", code, err)
	}
	token, err := o.Wait(context.Background(), "client123", code)
	if err != nil || token != "test-token" {
		t.Fatalf("token: %q, %v", token, err)
	}
	if requests != 2 {
		t.Fatalf("request count: %d", requests)
	}
}
