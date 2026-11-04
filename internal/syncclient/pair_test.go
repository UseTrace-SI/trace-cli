package syncclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPersonalPairingProofAndOrigin(t *testing.T) {
	cfg := Config{Endpoint: "https://trace.example", Remote: "solo/project", Mode: "personal"}
	var challenge string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var request map[string]string
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request["repo"] != cfg.Remote || request["device"] != "My laptop" || request["verification_path"] != "/connect" {
			t.Fatalf("invalid pairing: %+v", request)
		}
		challenge = request["challenge"]
		body, _ := json.Marshal(Pairing{ID: strings.Repeat("a", 43), VerificationURI: "https://trace.example/connect?request=" + strings.Repeat("a", 43), ExpiresAt: time.Now().Add(time.Minute).UnixMilli(), Interval: 1})
		return &http.Response{StatusCode: 201, Body: io.NopCloser(strings.NewReader(string(body))), Header: make(http.Header)}, nil
	})}
	flow, err := StartPairing(context.Background(), client, cfg, "My laptop")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(flow.verifier))
	if challenge != hex.EncodeToString(sum[:]) {
		t.Fatal("challenge is not bound to the private verifier")
	}
	encoded, _ := json.Marshal(flow)
	if strings.Contains(string(encoded), flow.verifier) {
		t.Fatal("private verifier was included in public connection metadata")
	}
	malicious := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := json.Marshal(Pairing{ID: strings.Repeat("a", 43), VerificationURI: "https://evil.example/app/connect.html?request=" + strings.Repeat("a", 43), ExpiresAt: time.Now().Add(time.Minute).UnixMilli(), Interval: 1})
		return &http.Response{StatusCode: 201, Body: io.NopCloser(strings.NewReader(string(body))), Header: make(http.Header)}, nil
	})}
	if _, err := StartPairing(context.Background(), malicious, cfg, "My laptop"); err == nil {
		t.Fatal("accepted an approval URL on another origin")
	}
	if cfg.CredentialKey() == (Config{Endpoint: "https://other.example", Remote: cfg.Remote, Mode: "personal"}).CredentialKey() || cfg.CredentialKey() == cfg.Remote {
		t.Fatal("credentials are not isolated by service")
	}
}

func TestPersonalPairingWaitCancellationAndCredential(t *testing.T) {
	flow := Pairing{ID: strings.Repeat("a", 43), verifier: strings.Repeat("b", 43), ExpiresAt: time.Now().Add(time.Minute).UnixMilli(), Interval: 1}
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var request map[string]string
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request["verifier"] != flow.verifier {
			t.Fatal("proof was not sent to the polling endpoint")
		}
		data, _ := json.Marshal(Credentials{AccessToken: "trace_" + strings.Repeat("x", 43), ExpiresAt: time.Now().Add(time.Hour).UnixMilli(), ConnectionID: "device-1"})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(data))), Header: make(http.Header)}, nil
	})}
	credentials, err := WaitPairing(context.Background(), client, "https://trace.example", flow)
	if err != nil || credentials.ConnectionID != "device-1" {
		t.Fatalf("credentials: %+v, %v", credentials, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := WaitPairing(ctx, client, "https://trace.example", flow); err == nil {
		t.Fatal("cancelled connection succeeded")
	}
}

func TestPersonalPairingCanonicalAndLegacyPaths(t *testing.T) {
	cfg := Config{Endpoint: "https://trace.example", Remote: "solo/project", Mode: "personal"}
	id := strings.Repeat("a", 43)
	for _, tc := range []struct {
		path string
		ok   bool
	}{
		{"/connect", true},
		{"/app/connect.html", true}, // Existing servers remain compatible.
		{"/workspace", false},
		{"/connect/other", false},
		{"/app/other.html", false},
	} {
		t.Run(tc.path, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				data, _ := json.Marshal(Pairing{ID: id, VerificationURI: "https://trace.example" + tc.path + "?request=" + id, ExpiresAt: time.Now().Add(time.Minute).UnixMilli(), Interval: 1})
				return &http.Response{StatusCode: 201, Body: io.NopCloser(strings.NewReader(string(data))), Header: make(http.Header)}, nil
			})}
			_, err := StartPairing(context.Background(), client, cfg, "My laptop")
			if (err == nil) != tc.ok {
				t.Fatalf("pairing URL %s: %v", tc.path, err)
			}
		})
	}
}
