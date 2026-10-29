package syncclient

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type DeviceCode struct {
	Code      string `json:"device_code"`
	UserCode  string `json:"user_code"`
	VerifyURL string `json:"verification_uri"`
	ExpiresIn int    `json:"expires_in"`
	Interval  int    `json:"interval"`
}

type BrowserCode struct {
	State       string
	Verifier    string
	Challenge   string
	RedirectURI string
	AuthURL     string
}

type OAuth struct {
	Client *http.Client
	Base   string
	App    bool
}

func (o OAuth) client() *http.Client {
	if o.Client != nil {
		return o.Client
	}
	return &http.Client{Timeout: 10 * time.Second}
}

func (o OAuth) base() string {
	if o.Base != "" {
		return strings.TrimRight(o.Base, "/")
	}
	return "https://github.com"
}

func (o OAuth) form(ctx context.Context, path string, values url.Values, output any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.base()+path, strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := o.client().Do(req)
	if err != nil {
		return fmt.Errorf("contacting GitHub OAuth: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub OAuth returned %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(output); err != nil {
		return fmt.Errorf("decoding GitHub OAuth response: %w", err)
	}
	return nil
}

func (o OAuth) Start(ctx context.Context, clientID string) (DeviceCode, error) {
	if clientID == "" {
		return DeviceCode{}, errors.New("GitHub OAuth client ID is required")
	}
	var code DeviceCode
	scope := "read:user repo"
	if o.App {
		scope = "offline_access"
	}
	err := o.form(ctx, "/login/device/code", url.Values{"client_id": {clientID}, "scope": {scope}}, &code)
	if err != nil {
		return DeviceCode{}, err
	}
	if code.Code == "" || code.UserCode == "" || code.VerifyURL == "" {
		return DeviceCode{}, errors.New("GitHub returned an incomplete device code")
	}
	return code, nil
}

func (OAuth) BrowserAuthorization(clientID, redirectURI string) (BrowserCode, error) {
	if clientID == "" || redirectURI == "" {
		return BrowserCode{}, errors.New("GitHub client ID and callback URL are required")
	}
	random := func(n int) (string, error) {
		b := make([]byte, n)
		if _, err := rand.Read(b); err != nil {
			return "", err
		}
		return base64.RawURLEncoding.EncodeToString(b), nil
	}
	state, err := random(32)
	if err != nil {
		return BrowserCode{}, fmt.Errorf("creating OAuth state: %w", err)
	}
	verifier, err := random(32)
	if err != nil {
		return BrowserCode{}, fmt.Errorf("creating PKCE verifier: %w", err)
	}
	challengeBytes := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeBytes[:])
	values := url.Values{
		"client_id": {clientID}, "redirect_uri": {redirectURI}, "state": {state},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"},
		"scope": {"offline_access"},
	}
	return BrowserCode{State: state, Verifier: verifier, Challenge: challenge, RedirectURI: redirectURI,
		AuthURL: "https://github.com/login/oauth/authorize?" + values.Encode()}, nil
}

func (o OAuth) ExchangeWithPlatform(ctx context.Context, endpoint string, flow BrowserCode, code string) (Credentials, error) {
	return o.platformTokenRequest(ctx, strings.TrimRight(endpoint, "/")+"/auth/cli/exchange", map[string]string{
		"code": code, "redirect_uri": flow.RedirectURI, "code_verifier": flow.Verifier,
	})
}

func (o OAuth) RefreshWithPlatform(ctx context.Context, endpoint string, previous Credentials) (Credentials, error) {
	if previous.RefreshToken == "" || previous.RefreshExpiresAt <= time.Now().UnixMilli() {
		return Credentials{}, errors.New("GitHub login expired; run trace login again")
	}
	return o.platformTokenRequest(ctx, strings.TrimRight(endpoint, "/")+"/auth/cli/refresh", map[string]string{
		"refresh_token": previous.RefreshToken,
	})
}

func (o OAuth) platformTokenRequest(ctx context.Context, target string, input map[string]string) (Credentials, error) {
	body, err := json.Marshal(input)
	if err != nil {
		return Credentials{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(string(body)))
	if err != nil {
		return Credentials{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := o.client().Do(req)
	if err != nil {
		return Credentials{}, fmt.Errorf("contacting Trace authentication service: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Credentials{}, fmt.Errorf("trace authentication service returned %s", resp.Status)
	}
	var credentials Credentials
	if err := json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&credentials); err != nil {
		return Credentials{}, fmt.Errorf("decoding Trace authentication response: %w", err)
	}
	if credentials.AccessToken == "" || credentials.RefreshToken == "" || credentials.ExpiresAt <= 0 || credentials.RefreshExpiresAt <= 0 {
		return Credentials{}, errors.New("trace authentication service returned incomplete credentials")
	}
	return credentials, nil
}

func (o OAuth) Wait(ctx context.Context, clientID string, code DeviceCode) (string, error) {
	credentials, err := o.WaitCredentials(ctx, clientID, code)
	if err != nil {
		return "", err
	}
	return credentials.AccessToken, nil
}

func credentialsFromResponse(token, refresh string, expiresIn, refreshExpiresIn int) (Credentials, error) {
	if token == "" {
		return Credentials{}, errors.New("GitHub returned no OAuth token")
	}
	credentials := Credentials{AccessToken: token}
	if expiresIn == 0 {
		return credentials, nil
	}
	if expiresIn < 0 || refresh == "" || refreshExpiresIn <= 0 {
		return Credentials{}, errors.New("GitHub returned incomplete expiring credentials")
	}
	now := time.Now()
	credentials.RefreshToken = refresh
	credentials.ExpiresAt = now.Add(time.Duration(expiresIn) * time.Second).UnixMilli()
	credentials.RefreshExpiresAt = now.Add(time.Duration(refreshExpiresIn) * time.Second).UnixMilli()
	return credentials, nil
}

func (o OAuth) WaitCredentials(ctx context.Context, clientID string, code DeviceCode) (Credentials, error) {
	if code.ExpiresIn <= 0 {
		return Credentials{}, errors.New("device code has no expiry")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(code.ExpiresIn)*time.Second)
	defer cancel()
	interval := time.Duration(max(code.Interval, 5)) * time.Second
	for {
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Credentials{}, ctx.Err()
		case <-timer.C:
		}
		var response struct {
			Token            string `json:"access_token"`
			Refresh          string `json:"refresh_token"`
			ExpiresIn        int    `json:"expires_in"`
			RefreshExpiresIn int    `json:"refresh_token_expires_in"`
			Error            string `json:"error"`
			Interval         int    `json:"interval"`
		}
		err := o.form(ctx, "/login/oauth/access_token", url.Values{
			"client_id": {clientID}, "device_code": {code.Code},
			"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"},
		}, &response)
		if err != nil {
			return Credentials{}, err
		}
		if response.Token != "" {
			credentials, err := credentialsFromResponse(response.Token, response.Refresh, response.ExpiresIn, response.RefreshExpiresIn)
			if err != nil {
				return Credentials{}, err
			}
			if o.App && credentials.ExpiresAt == 0 {
				return Credentials{}, errors.New("GitHub App returned a token without refresh credentials")
			}
			return credentials, nil
		}
		switch response.Error {
		case "authorization_pending":
		case "slow_down":
			interval += 5 * time.Second
			if response.Interval > 0 {
				interval = max(interval, time.Duration(response.Interval)*time.Second)
			}
		default:
			if response.Error == "" {
				return Credentials{}, errors.New("GitHub returned no OAuth token")
			}
			return Credentials{}, fmt.Errorf("GitHub device authorization failed: %s", response.Error)
		}
	}
}

func (o OAuth) Refresh(ctx context.Context, clientID string, previous Credentials) (Credentials, error) {
	if clientID == "" || previous.RefreshToken == "" || previous.RefreshExpiresAt <= time.Now().UnixMilli() {
		return Credentials{}, errors.New("GitHub login expired; run trace login again")
	}
	var response struct {
		Token            string `json:"access_token"`
		Refresh          string `json:"refresh_token"`
		ExpiresIn        int    `json:"expires_in"`
		RefreshExpiresIn int    `json:"refresh_token_expires_in"`
		Error            string `json:"error"`
	}
	if err := o.form(ctx, "/login/oauth/access_token", url.Values{
		"client_id": {clientID}, "grant_type": {"refresh_token"}, "refresh_token": {previous.RefreshToken},
	}, &response); err != nil {
		return Credentials{}, err
	}
	if response.Error != "" {
		return Credentials{}, fmt.Errorf("GitHub login refresh failed: %s", response.Error)
	}
	credentials, err := credentialsFromResponse(response.Token, response.Refresh, response.ExpiresIn, response.RefreshExpiresIn)
	if err != nil {
		return Credentials{}, err
	}
	if credentials.ExpiresAt == 0 {
		return Credentials{}, errors.New("GitHub App returned a token without refresh credentials")
	}
	return credentials, nil
}
