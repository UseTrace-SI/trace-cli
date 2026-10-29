package cli

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/UseTrace-SI/trace-cli/internal/syncclient"
)

func TestGitHubRemote(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"https://github.com/owner/repo.git", "owner/repo"},
		{"git@github.com:owner/repo.git", "owner/repo"},
		{"ssh://git@github.com/owner/repo", "owner/repo"},
		{"https://github.com.evil.test/owner/repo", ""},
		{"https://github.com/owner/repo/extra", ""},
		{"https://user:secret@github.com/owner/repo", ""},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, err := githubRemote(tc.input)
			if got != tc.want || (err != nil) != (tc.want == "") {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestBrowserCommand(t *testing.T) {
	for _, tc := range []struct {
		goos string
		want []string
	}{
		{"darwin", []string{"open", "https://github.com/login/device"}},
		{"linux", []string{"xdg-open", "https://github.com/login/device"}},
		{"windows", []string{"rundll32", "url.dll,FileProtocolHandler", "https://github.com/login/device"}},
	} {
		t.Run(tc.goos, func(t *testing.T) {
			got := browserCommand(tc.goos, "https://github.com/login/device")
			if !reflect.DeepEqual(got.Args, tc.want) {
				t.Fatalf("got %v, want %v", got.Args, tc.want)
			}
		})
	}
}

func TestBrowserCallbackVerifiesStateAndReceivesCode(t *testing.T) {
	flow, err := (syncclient.OAuth{}).BrowserAuthorization("client123", "http://127.0.0.1:43210/oauth/callback")
	if err != nil {
		t.Fatal(err)
	}
	callback := make(chan browserCallback, 1)
	request := httptest.NewRequest(http.MethodGet, "/oauth/callback?code=temporary-code&state="+flow.State, nil)
	response := httptest.NewRecorder()
	browserCallbackHandler(flow, callback).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("callback status = %d", response.Code)
	}
	got := <-callback
	if got.err != nil || got.code != "temporary-code" {
		t.Fatalf("callback = %+v", got)
	}
}

func TestBrowserCallbackRejectsStateMismatch(t *testing.T) {
	flow, err := (syncclient.OAuth{}).BrowserAuthorization("client123", "http://127.0.0.1:43210/oauth/callback")
	if err != nil {
		t.Fatal(err)
	}
	callback := make(chan browserCallback, 1)
	request := httptest.NewRequest(http.MethodGet, "/oauth/callback?code=temporary-code&state=wrong", nil)
	response := httptest.NewRecorder()
	browserCallbackHandler(flow, callback).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("callback status = %d, want 400", response.Code)
	}
	got := <-callback
	if got.err == nil || got.code != "" {
		t.Fatalf("callback accepted mismatched state: %+v", got)
	}
}

type loginRoundTrip func(*http.Request) (*http.Response, error)

func (f loginRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDiscoverLoginConfig(t *testing.T) {
	for _, tc := range []struct {
		name, body       string
		status           int
		wantID           string
		wantApp, wantErr bool
	}{
		{"app", `{"clientId":"client","authMode":"app"}`, 200, "client", true, false},
		{"oauth", `{"clientId":"client","authMode":"oauth"}`, 200, "client", false, false},
		{"missing", `{"clientId":"","authMode":"oauth"}`, 200, "", false, true},
		{"unknown", `{"clientId":"client","authMode":"other"}`, 200, "", false, true},
		{"failure", `{}`, 500, "", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: loginRoundTrip(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/api/config" {
					t.Errorf("unexpected path %s", r.URL.Path)
				}
				return &http.Response{StatusCode: tc.status, Status: http.StatusText(tc.status), Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})}
			id, app, err := discoverLoginConfig(context.Background(), "https://example.com", client)
			if id != tc.wantID || app != tc.wantApp || (err != nil) != tc.wantErr {
				t.Fatalf("got %q, %v, %v", id, app, err)
			}
		})
	}
}
