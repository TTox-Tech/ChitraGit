// Package i18n holds every user-facing string. UI code must call T instead
// of hard-coding text, so community translations can be added as files in
// locales/ without touching code (PRD F19).
package i18n

import (
	"embed"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
)

//go:embed locales/*.toml
var localeFS embed.FS

const fallback = "en"

var (
	mu              sync.RWMutex
	active, english map[string]string
)

func init() {
	english = mustLoad(fallback)
	active = english
}

func mustLoad(lang string) map[string]string {
	m, err := load(lang)
	if err != nil {
		panic(fmt.Sprintf("i18n: cannot load %s: %v", lang, err))
	}
	return m
}

func load(lang string) (map[string]string, error) {
	data, err := localeFS.ReadFile("locales/" + lang + ".toml")
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	if _, err := toml.Decode(string(data), &m); err != nil {
		return nil, err
	}
	return m, nil
}

// SetLanguage switches the active catalogue. "" or "auto" uses $LANG.
// Unknown languages fall back to English and return false.
func SetLanguage(lang string) bool {
	if lang == "" || lang == "auto" {
		lang = fromEnv()
	}
	mu.Lock()
	defer mu.Unlock()
	if lang == fallback {
		active = english
		return true
	}
	m, err := load(lang)
	if err != nil {
		active = english
		return false
	}
	active = m
	return true
}

func fromEnv() string {
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(k); v != "" && v != "C" && v != "POSIX" {
			code, _, _ := strings.Cut(v, "_")
			code, _, _ = strings.Cut(code, ".")
			return strings.ToLower(code)
		}
	}
	return fallback
}

// T returns the message for key, with {name} placeholders replaced by
// the given name/value pairs: T("scan.found", "count", 42).
// Missing translations fall back to English; missing keys return the key.
func T(key string, pairs ...any) string {
	mu.RLock()
	msg, ok := active[key]
	if !ok {
		msg, ok = english[key]
	}
	mu.RUnlock()
	if !ok {
		return key
	}
	for i := 0; i+1 < len(pairs); i += 2 {
		msg = strings.ReplaceAll(msg, "{"+fmt.Sprint(pairs[i])+"}", fmt.Sprint(pairs[i+1]))
	}
	return msg
}
