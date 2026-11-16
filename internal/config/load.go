package config

import (
	"bytes"
	"encoding"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"
	"unicode"

	"go.yaml.in/yaml/v3"
)

// EnvPrefix starts every environment override.
const EnvPrefix = "FUKKU_"

// Environment variables with the prefix that are not settings.
var reservedEnv = map[string]bool{
	"FUKKU_CONFIG": true, // path of the YAML file
	"FUKKU_DEV":    true, // set inside the development image
}

// Options say where Load reads from. Zero values mean the process environment and disk.
type Options struct {
	// File is the YAML file path; empty means no file (defaults plus environment only).
	File string
	// Environ is a list of KEY=VALUE pairs; nil means os.Environ().
	Environ []string
	// ReadFile reads files named by *_FILE variables and by File; nil means os.ReadFile.
	ReadFile func(string) ([]byte, error)
}

// Load returns the defaults, overlaid by the YAML file, overlaid by FUKKU_* variables.
// It does not validate; call Validate with the process role afterwards.
func Load(opts Options) (Config, error) {
	if opts.Environ == nil {
		opts.Environ = os.Environ()
	}
	if opts.ReadFile == nil {
		opts.ReadFile = os.ReadFile
	}
	cfg := Defaults()

	if opts.File != "" {
		data, err := opts.ReadFile(opts.File)
		if err != nil {
			return Config{}, fmt.Errorf("read config file: %w", err)
		}
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
			return Config{}, fmt.Errorf("config file %s: %w", opts.File, err)
		}
	}

	if err := applyEnv(&cfg, opts); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Leaf describes one setting.
type Leaf struct {
	Key     string // dotted YAML path, e.g. server.listenAddress
	Env     string // environment override, e.g. FUKKU_SERVER_LISTEN_ADDRESS
	Secret  bool   // also accepts <Env>_FILE; never printed
	Default string // the default, formatted as in docs/settings.md
	value   reflect.Value
}

// Leaves lists every setting of cfg in declaration order.
func Leaves(cfg *Config) []Leaf {
	var out []Leaf
	walk(reflect.ValueOf(cfg).Elem(), nil, &out)
	return out
}

var (
	secretType          = reflect.TypeFor[Secret]()
	textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()
)

func isLeafType(t reflect.Type) bool {
	if t.Kind() != reflect.Struct {
		return true
	}
	return reflect.PointerTo(t).Implements(textUnmarshalerType)
}

func walk(v reflect.Value, path []string, out *[]Leaf) {
	t := v.Type()
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if name == "" || name == "-" {
			continue
		}
		p := append(append([]string(nil), path...), name)
		fv := v.Field(i)
		if !isLeafType(f.Type) {
			walk(fv, p, out)
			continue
		}
		*out = append(*out, Leaf{
			Key:     strings.Join(p, "."),
			Env:     envName(p),
			Secret:  f.Type == secretType,
			Default: Format(fv),
			value:   fv,
		})
	}
}

func envName(path []string) string {
	var b strings.Builder
	b.WriteString(EnvPrefix)
	for i, part := range path {
		if i > 0 {
			b.WriteByte('_')
		}
		for j, r := range part {
			if j > 0 && unicode.IsUpper(r) {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToUpper(r))
		}
	}
	return b.String()
}

func applyEnv(cfg *Config, opts Options) error {
	env := map[string]string{}
	for _, kv := range opts.Environ {
		k, v, ok := strings.Cut(kv, "=")
		if ok && strings.HasPrefix(k, EnvPrefix) {
			env[k] = v
		}
	}

	known := map[string]bool{}
	var errs []error
	for _, leaf := range Leaves(cfg) {
		known[leaf.Env] = true
		raw, set := env[leaf.Env]
		if leaf.Secret {
			known[leaf.Env+"_FILE"] = true
			if path, ok := env[leaf.Env+"_FILE"]; ok {
				if set {
					errs = append(errs, fmt.Errorf("%s and %s_FILE are both set; use one", leaf.Env, leaf.Env))
					continue
				}
				data, err := opts.ReadFile(path)
				if err != nil {
					errs = append(errs, fmt.Errorf("%s_FILE: %w", leaf.Env, err))
					continue
				}
				raw, set = strings.TrimRight(string(data), "\r\n"), true
			}
		}
		if !set {
			continue
		}
		if err := setFromString(leaf.value, raw); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", leaf.Env, err))
		}
	}
	for k := range env {
		if !known[k] && !reservedEnv[k] {
			errs = append(errs, fmt.Errorf("%s is not a known setting (see docs/settings.md)", k))
		}
	}
	return errors.Join(errs...)
}

// setFromString sets a leaf from an environment value. Lists of strings are comma
// separated; other structured values (path mappings) are given as JSON or YAML.
func setFromString(v reflect.Value, raw string) error {
	if tu, ok := v.Addr().Interface().(encoding.TextUnmarshaler); ok {
		return tu.UnmarshalText([]byte(raw))
	}
	switch v.Kind() {
	case reflect.String:
		v.SetString(raw)
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("want true or false, got %q", raw)
		}
		v.SetBool(b)
	case reflect.Int, reflect.Int64:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return fmt.Errorf("want an integer, got %q", raw)
		}
		v.SetInt(n)
	case reflect.Float64:
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return fmt.Errorf("want a number, got %q", raw)
		}
		v.SetFloat(f)
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.String {
			list := []string{}
			for item := range strings.SplitSeq(raw, ",") {
				if item = strings.TrimSpace(item); item != "" {
					list = append(list, item)
				}
			}
			v.Set(reflect.ValueOf(list))
			return nil
		}
		ptr := reflect.New(v.Type())
		if err := yaml.Unmarshal([]byte(raw), ptr.Interface()); err != nil {
			return fmt.Errorf("want a JSON list: %w", err)
		}
		v.Set(ptr.Elem())
	default:
		return fmt.Errorf("unsupported setting type %s", v.Type())
	}
	return nil
}

// Format renders a setting value the way docs/settings.md writes defaults.
func Format(v reflect.Value) string {
	if v.Type() == secretType {
		return ""
	}
	if s, ok := v.Interface().(fmt.Stringer); ok {
		return s.String()
	}
	switch v.Kind() {
	case reflect.String:
		return v.String()
	case reflect.Bool:
		return strconv.FormatBool(v.Bool())
	case reflect.Int, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10)
	case reflect.Float64:
		return strconv.FormatFloat(v.Float(), 'g', -1, 64)
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.String {
			items := make([]string, v.Len())
			for i := range items {
				items[i] = v.Index(i).String()
			}
			return strings.Join(items, ", ")
		}
		if v.Len() == 0 {
			return ""
		}
		return fmt.Sprintf("%d entries", v.Len())
	}
	return fmt.Sprint(v.Interface())
}
