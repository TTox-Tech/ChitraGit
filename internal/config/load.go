package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/TTox-Tech/chitragit/internal/core"
)

// Sources tells Load which files to read. Empty paths are skipped.
type Sources struct {
	UserFile    string
	ArchiveFile string
	// Environ defaults to os.Environ() when nil.
	Environ []string
}

// Warning is a non-fatal problem found while loading, such as an unknown key.
type Warning struct {
	File    string
	Message string
}

func (w Warning) String() string {
	if w.File == "" {
		return w.Message
	}
	return w.File + ": " + w.Message
}

// Load merges defaults, files and environment variables, then validates.
func Load(src Sources) (*Config, []Warning, error) {
	cfg := Defaults()
	var warnings []Warning

	for _, f := range []string{src.UserFile, src.ArchiveFile} {
		if f == "" {
			continue
		}
		w, err := mergeFile(cfg, f)
		if err != nil {
			return nil, warnings, err
		}
		warnings = append(warnings, w...)
	}

	env := src.Environ
	if env == nil {
		env = os.Environ()
	}
	if err := applyEnv(cfg, env); err != nil {
		return nil, warnings, err
	}

	if err := cfg.Validate(); err != nil {
		return nil, warnings, err
	}
	return cfg, warnings, nil
}

func mergeFile(cfg *Config, path string) ([]Warning, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, &core.Error{Category: core.CatConfig, Message: "cannot read config file " + path, Err: err}
	}
	md, err := toml.Decode(string(data), cfg)
	if err != nil {
		return nil, &core.Error{
			Category: core.CatConfig,
			Message:  "invalid config file " + path,
			Detail:   err.Error(),
			Hint:     "fix the file or run `chitragit config validate` for details",
			Err:      err,
		}
	}
	// An unknown table and its keys are all reported as undecoded; report
	// only the most specific keys.
	undecoded := md.Undecoded()
	var warnings []Warning
	for _, k := range undecoded {
		name := k.String()
		parent := false
		for _, other := range undecoded {
			if strings.HasPrefix(other.String(), name+".") {
				parent = true
				break
			}
		}
		if !parent {
			warnings = append(warnings, Warning{File: path, Message: fmt.Sprintf("unknown key %q ignored", name)})
		}
	}
	return warnings, nil
}

// envPrefix is the prefix for environment overrides:
// CHITRAGIT_<SECTION>_<KEY>, e.g. CHITRAGIT_SYNC_WORKERS=32.
const envPrefix = "CHITRAGIT_"

func applyEnv(cfg *Config, environ []string) error {
	byName := map[string]string{}
	for _, kv := range environ {
		k, v, ok := strings.Cut(kv, "=")
		if ok && strings.HasPrefix(k, envPrefix) {
			byName[k] = v
		}
	}
	if len(byName) == 0 {
		return nil
	}
	for _, key := range Keys() {
		name := envPrefix + strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
		v, ok := byName[name]
		if !ok {
			continue
		}
		if err := cfg.Set(key, v); err != nil {
			return &core.Error{Category: core.CatConfig, Message: "invalid environment variable " + name, Err: err}
		}
	}
	return nil
}

// Keys lists every settable dotted key, e.g. "sync.workers".
func Keys() []string {
	var keys []string
	t := reflect.TypeOf(Config{})
	for i := 0; i < t.NumField(); i++ {
		sec := t.Field(i)
		for j := 0; j < sec.Type.NumField(); j++ {
			keys = append(keys, tagName(sec)+"."+tagName(sec.Type.Field(j)))
		}
	}
	return keys
}

// Doc returns the documentation string for a dotted key, if any.
func Doc(key string) string {
	_, f, err := lookupField(reflect.ValueOf(Defaults()).Elem(), key)
	if err != nil {
		return ""
	}
	return f.Tag.Get("doc")
}

// Get returns the value of a dotted key formatted as text.
func (c *Config) Get(key string) (string, error) {
	v, _, err := lookupField(reflect.ValueOf(c).Elem(), key)
	if err != nil {
		return "", err
	}
	return formatValue(v), nil
}

// Set parses text into the field named by a dotted key.
// Lists are comma separated; maps use "k=v,k2=v2".
func (c *Config) Set(key, text string) error {
	v, _, err := lookupField(reflect.ValueOf(c).Elem(), key)
	if err != nil {
		return err
	}
	return parseInto(v, text)
}

func tagName(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get("toml"), ",")
	if name == "" {
		return strings.ToLower(f.Name)
	}
	return name
}

func lookupField(root reflect.Value, key string) (reflect.Value, reflect.StructField, error) {
	secName, fieldName, ok := strings.Cut(key, ".")
	if !ok {
		return reflect.Value{}, reflect.StructField{}, fmt.Errorf("unknown key %q (expected section.key)", key)
	}
	rt := root.Type()
	for i := 0; i < rt.NumField(); i++ {
		if tagName(rt.Field(i)) != secName {
			continue
		}
		sec := root.Field(i)
		st := sec.Type()
		for j := 0; j < st.NumField(); j++ {
			if tagName(st.Field(j)) == fieldName {
				return sec.Field(j), st.Field(j), nil
			}
		}
	}
	return reflect.Value{}, reflect.StructField{}, fmt.Errorf("unknown key %q", key)
}

var durationType = reflect.TypeOf(Duration(0))

func formatValue(v reflect.Value) string {
	if v.Type() == durationType {
		return Duration(v.Int()).String()
	}
	switch v.Kind() {
	case reflect.Slice:
		parts := make([]string, v.Len())
		for i := range parts {
			parts[i] = fmt.Sprint(v.Index(i).Interface())
		}
		return strings.Join(parts, ",")
	case reflect.Map:
		keys := v.MapKeys()
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%v=%v", k.Interface(), v.MapIndex(k).Interface()))
		}
		slices.Sort(parts)
		return strings.Join(parts, ",")
	}
	return fmt.Sprint(v.Interface())
}

func parseInto(v reflect.Value, text string) error {
	text = strings.TrimSpace(text)
	if v.Type() == durationType {
		d, err := ParseDuration(text)
		if err != nil {
			return err
		}
		v.SetInt(int64(d))
		return nil
	}
	switch v.Kind() {
	case reflect.String:
		v.SetString(text)
	case reflect.Int:
		n, err := strconv.Atoi(text)
		if err != nil {
			return fmt.Errorf("%q is not a whole number", text)
		}
		v.SetInt(int64(n))
	case reflect.Bool:
		b, err := strconv.ParseBool(text)
		if err != nil {
			return fmt.Errorf("%q is not true/false", text)
		}
		v.SetBool(b)
	case reflect.Slice:
		var items []string
		for _, s := range strings.Split(text, ",") {
			if s = strings.TrimSpace(s); s != "" {
				items = append(items, s)
			}
		}
		v.Set(reflect.ValueOf(items))
	case reflect.Map:
		m := map[string]int{}
		for _, pair := range strings.Split(text, ",") {
			pair = strings.TrimSpace(pair)
			if pair == "" {
				continue
			}
			k, val, ok := strings.Cut(pair, "=")
			n, err := strconv.Atoi(strings.TrimSpace(val))
			if !ok || err != nil {
				return fmt.Errorf("%q: expected host=number", pair)
			}
			m[strings.TrimSpace(k)] = n
		}
		v.Set(reflect.ValueOf(m))
	default:
		return fmt.Errorf("unsupported type %s", v.Type())
	}
	return nil
}
