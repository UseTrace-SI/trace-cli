package syncclient

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestSyncRequestsUseCurrentToken(t *testing.T) {
	requests := 0
	client := &Client{
		Config:        Config{Endpoint: "https://trace.example", Remote: "owner/repo", Mode: "team"},
		Token:         "expired-token",
		TokenProvider: func(context.Context) (string, error) { return "renewed-token", nil },
		HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			requests++
			if r.Header.Get("Authorization") != "Bearer renewed-token" {
				t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
			}
			return response(`{}`), nil
		})},
	}
	if err := client.post(context.Background(), "/v1/sync/rows", map[string]any{}, nil); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := client.get(context.Background(), "/v1/sync/rows", url.Values{}, &result); err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d", requests)
	}
}

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

func TestGitHubAppDeviceScopeAndRefresh(t *testing.T) {
	o := OAuth{App: true, Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		values, err := url.ParseQuery(string(body))
		if err != nil {
			t.Fatal(err)
		}
		switch r.URL.Path {
		case "/login/device/code":
			if values.Get("scope") != "offline_access" {
				t.Fatalf("GitHub App scope = %q", values.Get("scope"))
			}
			return response(`{"device_code":"device","user_code":"ABCD","verification_uri":"https://github.com/login/device","expires_in":10}`), nil
		case "/login/oauth/access_token":
			if values.Get("grant_type") != "refresh_token" || values.Get("refresh_token") != "old-refresh" {
				t.Fatal("invalid refresh request")
			}
			return response(`{"access_token":"new-token","refresh_token":"new-refresh","expires_in":28800,"refresh_token_expires_in":15897600}`), nil
		default:
			t.Fatalf("unexpected OAuth path %q", r.URL.Path)
			return nil, nil
		}
	})}}
	if _, err := o.Start(context.Background(), "app-id"); err != nil {
		t.Fatal(err)
	}
	previous := Credentials{AccessToken: "old-token", RefreshToken: "old-refresh", ExpiresAt: time.Now().Add(-time.Minute).UnixMilli(), RefreshExpiresAt: time.Now().Add(time.Hour).UnixMilli()}
	updated, err := o.Refresh(context.Background(), "app-id", previous)
	if err != nil {
		t.Fatal(err)
	}
	if updated.AccessToken != "new-token" || updated.RefreshToken != "new-refresh" || updated.ExpiresAt <= time.Now().UnixMilli() {
		t.Fatalf("refresh result = %+v", updated)
	}
}

func TestCredentialsEncodingSupportsLegacyToken(t *testing.T) {
	legacy, err := decodeCredentials("old-token\n")
	if err != nil || legacy.AccessToken != "old-token" {
		t.Fatalf("legacy = %+v, %v", legacy, err)
	}
	want := Credentials{AccessToken: "new-token", RefreshToken: "new-refresh", ExpiresAt: 123, RefreshExpiresAt: 456}
	encoded, err := want.encode()
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeCredentials(encoded)
	if err != nil || got != want {
		t.Fatalf("round trip = %+v, %v", got, err)
	}
}
