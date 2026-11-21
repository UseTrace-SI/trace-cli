package syncclient

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestBrowserOAuthUsesStateAndPKCE(t *testing.T) {
	flow, err := (OAuth{}).BrowserAuthorization("client123", "http://127.0.0.1:43210/oauth/callback")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(flow.AuthURL)
	if err != nil {
		t.Fatal(err)
	}
	values := parsed.Query()
	if parsed.Host != "github.com" || parsed.Path != "/login/oauth/authorize" || values.Get("client_id") != "client123" || values.Get("state") != flow.State {
		t.Fatalf("unexpected authorization URL %s", flow.AuthURL)
	}
	digest := sha256.Sum256([]byte(flow.Verifier))
	if values.Get("code_challenge_method") != "S256" || values.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(digest[:]) {
		t.Fatal("authorization URL does not contain the verifier's S256 challenge")
	}
	if values.Get("redirect_uri") != flow.RedirectURI {
		t.Fatal("authorization URL has a different callback URI")
	}
}

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

// GitHub OAuth responses must be size-capped before JSON decoding, like the
// platform token responses, so a hostile or broken endpoint cannot make the
// client buffer an unbounded body.
func TestFormResponseSizeLimit(t *testing.T) {
	for _, tc := range []struct {
		name    string
		body    string
		wantErr bool
	}{
		{
			name: "small response decodes",
			body: `{"device_code":"device","user_code":"ABCD","verification_uri":"https://github.com/login/device","expires_in":10}`,
		},
		{
			name:    "oversized response is rejected",
			body:    `{"device_code":"` + strings.Repeat("d", 70000) + `"}`,
			wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := OAuth{Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return response(tc.body), nil
			})}}
			var out DeviceCode
			err := o.form(context.Background(), "/login/device/code", url.Values{"client_id": {"id"}}, &out)
			if (err != nil) != tc.wantErr {
				t.Fatalf("form error = %v, wantErr %t", err, tc.wantErr)
			}
		})
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

// Token requests carry refresh tokens, authorization codes and PKCE verifiers
// in their bodies. A 307 or 308 from the endpoint must not resend them to the
// Location, which may name another origin or plain HTTP.
func TestOAuthRequestsDoNotFollowRedirects(t *testing.T) {
	resent := make(chan string, 16)
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		resent <- r.Method + " " + r.URL.Path + " " + string(body)
		w.WriteHeader(http.StatusTeapot)
	}))
	defer elsewhere.Close()
	ctx := context.Background()
	previous := Credentials{AccessToken: "old-token", RefreshToken: "refresh-secret", ExpiresAt: 1, RefreshExpiresAt: time.Now().Add(time.Hour).UnixMilli()}
	flow := BrowserCode{RedirectURI: "http://127.0.0.1:1/oauth/callback", Verifier: "pkce-verifier-secret"}
	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, elsewhere.URL+"/collect", status)
		}))
		o := OAuth{App: true, Base: origin.URL}
		for _, tc := range []struct {
			name string
			call func() error
		}{
			{"platform refresh", func() error { _, err := o.RefreshWithPlatform(ctx, origin.URL, previous); return err }},
			{"platform code exchange", func() error { _, err := o.ExchangeWithPlatform(ctx, origin.URL, flow, "github-code"); return err }},
			{"GitHub refresh", func() error { _, err := o.Refresh(ctx, "client-id", previous); return err }},
			{"GitHub device code", func() error { _, err := o.Start(ctx, "client-id"); return err }},
		} {
			t.Run(fmt.Sprintf("%s/%d", tc.name, status), func(t *testing.T) {
				if err := tc.call(); err == nil {
					t.Fatal("redirect response accepted as a token response")
				}
				select {
				case request := <-resent:
					t.Fatalf("request resent after a %d redirect: %s", status, request)
				default:
				}
			})
		}
		origin.Close()
	}
}

// The Trace authentication service supplies tokens only. Pro key material in
// its response would let the server choose the key that seals Pro history.
func TestPlatformTokenResponseSuppliesOnlyTokens(t *testing.T) {
	later := time.Now().Add(time.Hour).UnixMilli()
	serverKey := base64.StdEncoding.EncodeToString(make([]byte, 32))
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "token", "refresh_token": "refresh", "expires_at": later, "refresh_expires_at": later,
			"pro_sync_key": serverKey, "pro_sync_salt": serverKey, "connection_id": "server-chosen", "scope": "account",
		})
	}))
	defer platform.Close()
	ctx := context.Background()
	want := Credentials{AccessToken: "token", RefreshToken: "refresh", ExpiresAt: later, RefreshExpiresAt: later}
	for _, tc := range []struct {
		name string
		call func() (Credentials, error)
	}{
		{"code exchange", func() (Credentials, error) {
			return OAuth{}.ExchangeWithPlatform(ctx, platform.URL, BrowserCode{RedirectURI: "http://127.0.0.1:1/oauth/callback", Verifier: "verifier"}, "code")
		}},
		{"refresh", func() (Credentials, error) {
			return OAuth{}.RefreshWithPlatform(ctx, platform.URL, Credentials{RefreshToken: "refresh", RefreshExpiresAt: later})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := tc.call(); err != nil || got != want {
				t.Fatalf("got %+v, %v; want only token fields", got, err)
			}
		})
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
