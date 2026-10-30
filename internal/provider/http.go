package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/version"
)

// HTTPDoer is the part of *http.Client the providers use.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// defaultHTTP respects HTTPS_PROXY/NO_PROXY and the system trust store
// (PRD §8.3); it never disables certificate checks.
var defaultHTTP = &http.Client{Timeout: 30 * time.Second}

// maxBody caps how much of a response is read, so a broken or hostile
// server cannot exhaust memory.
const maxBody = 32 << 20

type httpc struct {
	do    HTTPDoer
	token string
}

func newHTTP(do HTTPDoer, token string) *httpc {
	if do == nil {
		do = defaultHTTP
	}
	return &httpc{do: do, token: token}
}

// response is a finished request: status, headers and body.
type response struct {
	status int
	header http.Header
	body   []byte
	url    string // final URL, after redirects
}

// send performs one request. Status codes are returned, not turned into
// errors, because 404 and 451 are answers for rescue detection. Errors are
// network failures, rate limits and rejected tokens.
func (h *httpc) send(ctx context.Context, method, url, auth string, body any) (*response, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "chitragit/"+version.Version)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if h.token != "" && auth != "" {
		req.Header.Set("Authorization", auth+" "+h.token)
	}
	res, err := h.do.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, &core.Error{Category: core.CatTimeout, Message: "the provider API did not answer in time", Err: ctx.Err()}
		}
		return nil, &core.Error{Category: core.CatNetwork, Message: "could not reach the provider API", Detail: redact(err.Error(), h.token), Err: errors.New(redact(err.Error(), h.token))}
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, maxBody))
	if err != nil {
		return nil, &core.Error{Category: core.CatNetwork, Message: "the provider API connection broke", Err: err}
	}
	r := &response{status: res.StatusCode, header: res.Header, body: data, url: res.Request.URL.String()}
	if err := h.check(r); err != nil {
		return nil, err
	}
	return r, nil
}

// check turns the statuses that mean "cannot answer now" into errors.
func (h *httpc) check(r *response) error {
	switch {
	case r.status == http.StatusUnauthorized:
		if h.token == "" {
			return &core.Error{Category: core.CatAuth, Message: "this API needs a login",
				Hint: "run `chitragit auth login <provider>`"}
		}
		return &core.Error{Category: core.CatAuth, Message: "the saved token was rejected (expired or revoked)",
			Hint: "run `chitragit auth login <provider>` again"}
	case r.status == http.StatusTooManyRequests,
		r.status == http.StatusForbidden && (r.header.Get("X-RateLimit-Remaining") == "0" || r.header.Get("Retry-After") != ""):
		e := &core.Error{Category: core.CatRateLimited, Message: "the provider API rate limit was reached",
			Hint: "log in with `chitragit auth login` for a much higher limit, or try again later"}
		if wait := retryAfter(r.header); wait > 0 {
			e.Detail = "retry after " + wait.Round(time.Second).String()
		}
		return e
	case r.status >= 500:
		return &core.Error{Category: core.CatNetwork, Message: fmt.Sprintf("the provider API had an error (HTTP %d)", r.status)}
	}
	return nil
}

// retryAfter reads Retry-After or X-RateLimit-Reset.
func retryAfter(h http.Header) time.Duration {
	if s := h.Get("Retry-After"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			return time.Duration(n) * time.Second
		}
	}
	if s := h.Get("X-RateLimit-Reset"); s != "" {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return time.Until(time.Unix(n, 0))
		}
	}
	return 0
}

// getJSON GETs url and decodes a 200 body into v. Any other status is an
// error with the status in it.
func (h *httpc) getJSON(ctx context.Context, url, auth string, v any) (*response, error) {
	r, err := h.send(ctx, http.MethodGet, url, auth, nil)
	if err != nil {
		return nil, err
	}
	if r.status != http.StatusOK {
		return r, statusError(r)
	}
	return r, decode(r, v)
}

func statusError(r *response) error {
	cat := core.CatInternal
	switch r.status {
	case http.StatusNotFound:
		cat = core.CatNotFound
	case http.StatusForbidden:
		cat = core.CatAuth
	}
	return &core.Error{Category: cat, Message: fmt.Sprintf("the provider API answered HTTP %d", r.status),
		Detail: string(bytes.TrimSpace(firstN(r.body, 300)))}
}

func firstN(b []byte, n int) []byte {
	if len(b) > n {
		return b[:n]
	}
	return b
}

var nextLink = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

// next returns the rel="next" URL of a Link header, or "".
func next(h http.Header) string {
	if m := nextLink.FindStringSubmatch(h.Get("Link")); m != nil {
		return m[1]
	}
	return ""
}

// redact removes the token from text, in case a library echoes it.
func redact(text, token string) string {
	if token == "" {
		return text
	}
	return strings.ReplaceAll(text, token, "[token]")
}

// maxPages bounds pagination so a misbehaving API cannot loop forever.
// 1,000 pages of 100 is 100,000 repos per source.
const maxPages = 1000

// decode unmarshals a response body.
func decode(r *response, v any) error {
	if err := json.Unmarshal(r.body, v); err != nil {
		return &core.Error{Category: core.CatInternal, Message: "the provider API sent something unexpected", Err: err}
	}
	return nil
}
