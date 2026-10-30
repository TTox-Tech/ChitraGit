package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/TTox-Tech/chitragit/internal/core"
)

// GitHubClientID is the OAuth App used for `auth login github`. It is set
// at build time (-ldflags -X) or with CHITRAGIT_GITHUB_CLIENT_ID; without
// one, login falls back to pasting a personal access token.
var GitHubClientID = ""

// DeviceCode is what the user needs to approve a device login.
type DeviceCode struct {
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	DeviceCode      string `json:"device_code"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// Device runs the OAuth device flow (RFC 8628) against a GitHub host:
// it gets a code, calls show so the user can approve it in a browser, and
// polls until GitHub hands out a token.
type Device struct {
	HTTP     *http.Client
	ClientID string
	// Base is https://<host>; tests point it at a fake server.
	Base   string
	Scopes []string
	// sleep is time.Sleep that respects ctx; tests replace it.
	sleep func(ctx context.Context, d time.Duration) error
}

// Login performs the whole flow and returns the access token.
func (d *Device) Login(ctx context.Context, show func(DeviceCode)) (string, error) {
	if d.ClientID == "" {
		return "", &core.Error{Category: core.CatConfig, Message: "browser login is not set up in this build",
			Hint: "create a token at https://github.com/settings/tokens and run `chitragit auth login github --with-token`"}
	}
	if d.HTTP == nil {
		d.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	if d.sleep == nil {
		d.sleep = sleepCtx
	}
	var code DeviceCode
	if err := d.post(ctx, "/login/device/code", url.Values{
		"client_id": {d.ClientID}, "scope": {strings.Join(d.Scopes, " ")},
	}, &code); err != nil {
		return "", err
	}
	if code.DeviceCode == "" || code.UserCode == "" {
		return "", &core.Error{Category: core.CatInternal, Message: "GitHub did not send a login code"}
	}
	show(code)

	interval := time.Duration(max(code.Interval, 5)) * time.Second
	deadline := time.Now().Add(time.Duration(max(code.ExpiresIn, 60)) * time.Second)
	for time.Now().Before(deadline) {
		if err := d.sleep(ctx, interval); err != nil {
			return "", err
		}
		var tok struct {
			AccessToken string `json:"access_token"`
			Error       string `json:"error"`
			Interval    int    `json:"interval"`
		}
		if err := d.post(ctx, "/login/oauth/access_token", url.Values{
			"client_id": {d.ClientID}, "device_code": {code.DeviceCode},
			"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"},
		}, &tok); err != nil {
			return "", err
		}
		switch tok.Error {
		case "":
			if tok.AccessToken == "" {
				return "", &core.Error{Category: core.CatInternal, Message: "GitHub sent an empty token"}
			}
			return tok.AccessToken, nil
		case "authorization_pending":
		case "slow_down":
			interval = time.Duration(max(tok.Interval, int(interval/time.Second)+5)) * time.Second
		case "expired_token":
			return "", &core.Error{Category: core.CatAuth, Message: "the login code expired before it was approved",
				Hint: "run `chitragit auth login github` again"}
		case "access_denied":
			return "", &core.Error{Category: core.CatAuth, Message: "the login was cancelled in the browser"}
		default:
			return "", &core.Error{Category: core.CatAuth, Message: "GitHub refused the login: " + tok.Error}
		}
	}
	return "", &core.Error{Category: core.CatAuth, Message: "the login code expired before it was approved",
		Hint: "run `chitragit auth login github` again"}
}

func (d *Device) post(ctx context.Context, path string, form url.Values, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.Base+path, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err := d.HTTP.Do(req)
	if err != nil {
		return &core.Error{Category: core.CatNetwork, Message: "could not reach GitHub to log in", Err: err}
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return &core.Error{Category: core.CatNetwork, Message: "the connection to GitHub broke", Err: err}
	}
	if res.StatusCode != http.StatusOK {
		return &core.Error{Category: core.CatAuth, Message: fmt.Sprintf("GitHub login failed (HTTP %d)", res.StatusCode),
			Detail: string(body)}
	}
	if err := json.Unmarshal(body, v); err != nil {
		return &core.Error{Category: core.CatInternal, Message: "GitHub sent an unexpected login answer", Err: err}
	}
	return nil
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
