package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RulesReleaseURL is where `audit rules update` downloads signed packs:
// a rolling pre-release of ChitraGit's own repo, never marked latest, so
// installers and `releases/latest` are not affected.
const RulesReleaseURL = "https://github.com/TTox-Tech/ChitraGit/releases/download/kodekavach-rules/"

// Release asset names.
const (
	RulesAsset    = "kodekavach-rules.json"
	RulesSigAsset = "kodekavach-rules.json.sig"
)

const (
	rulesFile      = "rules.signed.json" // pack and signature together, replaced in one rename
	rulesCheckFile = "rules.checked"     // mtime = last time an update was looked for
	maxPackSize    = 4 << 20
)

// signedPack is how a downloaded pack is kept on disk. Keeping the pack
// and its signature in one file means a crash mid-write can never pair a
// new pack with an old signature.
type signedPack struct {
	Pack      []byte `json:"pack"` // exact bytes that were signed (base64 in JSON)
	Signature string `json:"signature"`
	Fetched   string `json:"fetched"`
}

// RulesDir is where downloaded packs live, inside the user cache folder.
func RulesDir(cacheDir string) string { return filepath.Join(cacheDir, "kodekavach") }

// LoadRules returns the pack to use: the downloaded one if it is newer
// than the built-in pack and its signature still checks out, otherwise
// the built-in pack. warn explains why a downloaded pack was not used; it
// never stops an audit.
func LoadRules(dir string) (pack *RulesPack, warn error) {
	builtin, err := BuiltinRules()
	if err != nil {
		return nil, err // a broken built-in pack is a build bug
	}
	sp, err := readSigned(filepath.Join(dir, rulesFile))
	if errors.Is(err, os.ErrNotExist) {
		return builtin, nil
	}
	if err != nil {
		return builtin, fmt.Errorf("downloaded rules pack is unreadable, using the built-in rules: %w", err)
	}
	if err := VerifyRules(sp.Pack, []byte(sp.Signature)); err != nil {
		if errors.Is(err, ErrNoRulesKey) {
			return builtin, nil
		}
		return builtin, fmt.Errorf("downloaded rules pack failed its signature check, using the built-in rules: %w", err)
	}
	p, err := ParseRules(sp.Pack)
	if err != nil {
		return builtin, fmt.Errorf("downloaded rules pack is not usable, using the built-in rules: %w", err)
	}
	if ComparePackVersions(p.Version, builtin.Version) <= 0 {
		return builtin, nil // this binary ships newer rules than the download
	}
	p.Source = filepath.Join(dir, rulesFile)
	return p, nil
}

func readSigned(name string) (*signedPack, error) {
	b, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	var sp signedPack
	if err := json.Unmarshal(b, &sp); err != nil {
		return nil, err
	}
	return &sp, nil
}

// RulesUpdate is the result of UpdateRules.
type RulesUpdate struct {
	Previous string `json:"previous"`
	Current  string `json:"current"`
	Updated  bool   `json:"updated"`
	Source   string `json:"source"`
}

// UpdateRules downloads the published pack and installs it if its
// signature is valid and it is newer than the pack in use. Nothing about
// the archive is sent; it is two plain GET requests. Any failure leaves
// the current pack untouched.
func UpdateRules(ctx context.Context, dir, baseURL string, client *http.Client) (*RulesUpdate, error) {
	if RulesPublicKey == "" {
		return nil, ErrNoRulesKey
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	if baseURL == "" {
		baseURL = RulesReleaseURL
	}
	pack, err := fetch(ctx, client, baseURL+RulesAsset)
	if err != nil {
		return nil, err
	}
	sig, err := fetch(ctx, client, baseURL+RulesSigAsset)
	if err != nil {
		return nil, err
	}
	return InstallRules(dir, pack, sig)
}

// InstallRules installs a pack and its signature (from a download, or
// files copied by hand to an offline machine) if the signature is valid
// and the pack is newer than the one in use.
func InstallRules(dir string, pack, sig []byte) (*RulesUpdate, error) {
	cur, _ := LoadRules(dir)
	if cur == nil {
		var err error
		if cur, err = BuiltinRules(); err != nil {
			return nil, err
		}
	}
	res := &RulesUpdate{Previous: cur.Version, Current: cur.Version, Source: cur.Source}
	if err := VerifyRules(pack, sig); err != nil {
		return nil, err
	}
	p, err := ParseRules(pack)
	if err != nil {
		return nil, fmt.Errorf("the rules pack was refused: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	touchChecked(dir)
	if ComparePackVersions(p.Version, cur.Version) <= 0 {
		return res, nil // never go back to an older pack
	}
	b, err := json.Marshal(signedPack{Pack: pack, Signature: strings.TrimSpace(string(sig)), Fetched: time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		return nil, err
	}
	if err := writeAtomic(filepath.Join(dir, rulesFile), b); err != nil {
		return nil, err
	}
	res.Current, res.Updated, res.Source = p.Version, true, filepath.Join(dir, rulesFile)
	return res, nil
}

func fetch(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "chitragit-kodekavach")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot download the rules pack: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cannot download the rules pack: %s returned %s", url, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxPackSize+1))
	if err != nil {
		return nil, fmt.Errorf("cannot download the rules pack: %w", err)
	}
	if len(b) > maxPackSize {
		return nil, errors.New("the published rules pack is too large and was refused")
	}
	return b, nil
}

func writeAtomic(name string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(name), ".rules-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), name)
}

func touchChecked(dir string) {
	name := filepath.Join(dir, rulesCheckFile)
	now := time.Now()
	if err := os.Chtimes(name, now, now); err != nil {
		_ = os.WriteFile(name, nil, 0o600)
	}
}

// RulesLastChecked is when an update was last looked for (zero if never).
func RulesLastChecked(dir string) time.Time {
	fi, err := os.Stat(filepath.Join(dir, rulesCheckFile))
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

// RulesUpdateDue reports whether the last check is older than every. A
// build without a signing key is never due.
func RulesUpdateDue(dir string, every time.Duration) bool {
	if RulesPublicKey == "" {
		return false
	}
	last := RulesLastChecked(dir)
	return last.IsZero() || time.Since(last) >= every
}

// MarkRulesChecked records that an update was looked for now, even if
// the lookup failed, so an offline machine does not retry on every audit.
func MarkRulesChecked(dir string) {
	if os.MkdirAll(dir, 0o700) == nil {
		touchChecked(dir)
	}
}
