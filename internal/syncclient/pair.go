package syncclient

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Pairing struct {
	ID              string `json:"id"`
	VerificationURI string `json:"verification_uri"`
	ExpiresAt       int64  `json:"expires_at"`
	Interval        int    `json:"interval"`
	verifier        string
}

func pairingRequest(ctx context.Context, client *http.Client, endpoint, path string, body any, out any) (int, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(endpoint, "/")+path, bytes.NewReader(data))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	response, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("contacting Trace: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return response.StatusCode, fmt.Errorf("trace server rejected connection request: %s", response.Status)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 8192)).Decode(out); err != nil {
		return 0, fmt.Errorf("reading Trace connection: %w", err)
	}
	return response.StatusCode, nil
}

func StartPairing(ctx context.Context, client *http.Client, cfg Config, device string) (Pairing, error) {
	if err := cfg.Validate(); err != nil {
		return Pairing{}, err
	}
	if cfg.Mode != "personal" {
		return Pairing{}, errors.New("browser pairing requires personal sync")
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return Pairing{}, fmt.Errorf("creating connection verifier: %w", err)
	}
	verifier := base64.RawURLEncoding.EncodeToString(random)
	sum := sha256.Sum256([]byte(verifier))
	var out Pairing
	_, err := pairingRequest(ctx, client, cfg.Endpoint, "/v1/cli/pair", map[string]string{"challenge": hex.EncodeToString(sum[:]), "repo": cfg.Remote, "device": device}, &out)
	if err != nil {
		return Pairing{}, err
	}
	verification, err := url.Parse(out.VerificationURI)
	expected, _ := url.Parse(cfg.Endpoint)
	if err != nil || verification.Scheme != "https" || verification.Host != expected.Host || verification.User != nil || verification.Fragment != "" || verification.Path != "/app/connect.html" || verification.Query().Get("request") != out.ID || len(out.ID) != 43 || out.ExpiresAt <= time.Now().UnixMilli() || out.ExpiresAt > time.Now().Add(15*time.Minute).UnixMilli() || out.Interval < 1 || out.Interval > 30 {
		return Pairing{}, errors.New("trace server returned an invalid connection request")
	}
	out.verifier = verifier
	return out, nil
}

func WaitPairing(ctx context.Context, client *http.Client, endpoint string, pairing Pairing) (Credentials, error) {
	ctx, cancel := context.WithDeadline(ctx, time.UnixMilli(pairing.ExpiresAt))
	defer cancel()
	if err := ctx.Err(); err != nil {
		return Credentials{}, fmt.Errorf("waiting for dashboard approval: %w", err)
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return Credentials{}, fmt.Errorf("waiting for dashboard approval: %w", ctx.Err())
		case <-timer.C:
			var out Credentials
			status, err := pairingRequest(ctx, client, endpoint, "/v1/cli/poll", map[string]string{"id": pairing.ID, "verifier": pairing.verifier}, &out)
			if err != nil {
				return Credentials{}, err
			}
			if status == http.StatusOK {
				if out.ConnectionID == "" || len(out.ConnectionID) > 128 || !strings.HasPrefix(out.AccessToken, "trace_") || len(out.AccessToken) != 49 || out.ExpiresAt <= time.Now().UnixMilli() {
					return Credentials{}, errors.New("trace server returned an invalid connection credential")
				}
				return out, nil
			}
			if status != http.StatusAccepted {
				return Credentials{}, errors.New("unexpected Trace connection response")
			}
			timer.Reset(time.Duration(pairing.Interval) * time.Second)
		}
	}
}
