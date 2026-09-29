// SPDX-License-Identifier: Apache-2.0

package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type dbConfig struct {
	Driver  string        `env:"DRIVER" default:"sqlite"`
	URL     string        `env:"URL,required"`
	MaxOpen int           `env:"MAX_OPEN" default:"20"`
	Timeout time.Duration `env:"TIMEOUT" default:"5s"`
}

func (d dbConfig) Validate() error {
	switch d.Driver {
	case "sqlite", "postgres", "mysql":
		return nil
	}
	return fmt.Errorf("unknown driver %q", d.Driver)
}

type appConfig struct {
	Name     string     `env:"APP_NAME" default:"anetos"`
	Debug    bool       `env:"APP_DEBUG"`
	Port     uint16     `env:"PORT" default:"8080"`
	Ratio    float64    `env:"RATIO" default:"0.5"`
	Hosts    []string   `env:"HOSTS"`
	Ports    []int      `env:"PORTS"`
	Level    slog.Level `env:"LOG_LEVEL" default:"info"`
	Optional *int       `env:"OPTIONAL"`
	Skipped  string     `env:"-"`
	DB       dbConfig   `prefix:"DB_"`
	Cache    *struct {
		TTL time.Duration `env:"CACHE_TTL" default:"1m"`
	}
	unexported string //nolint:unused // proves unexported fields are ignored
}

func TestBindFullStruct(t *testing.T) {
	src := Map{
		"APP_NAME":  "blog",
		"APP_DEBUG": "true",
		"HOSTS":     " a.example , b.example ,, ",
		"PORTS":     "1,2,3",
		"LOG_LEVEL": "DEBUG",
		"OPTIONAL":  "7",
		"DB_URL":    "file:app.db",
		"DB_DRIVER": "", // empty counts as unset: default applies
		"CACHE_TTL": "2m",
		"-":         "never read",
	}
	cfg, err := Get[appConfig](src)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if cfg.Name != "blog" || !cfg.Debug || cfg.Port != 8080 || cfg.Ratio != 0.5 {
		t.Errorf("scalars wrong: %+v", cfg)
	}
	if strings.Join(cfg.Hosts, "|") != "a.example|b.example" {
		t.Errorf("Hosts = %q", cfg.Hosts)
	}
	if fmt.Sprint(cfg.Ports) != "[1 2 3]" {
		t.Errorf("Ports = %v", cfg.Ports)
	}
	if cfg.Level != slog.LevelDebug {
		t.Errorf("Level = %v", cfg.Level)
	}
	if cfg.Optional == nil || *cfg.Optional != 7 {
		t.Errorf("Optional = %v", cfg.Optional)
	}
	if cfg.DB.Driver != "sqlite" || cfg.DB.URL != "file:app.db" || cfg.DB.MaxOpen != 20 || cfg.DB.Timeout != 5*time.Second {
		t.Errorf("DB = %+v", cfg.DB)
	}
	if cfg.Cache == nil || cfg.Cache.TTL != 2*time.Minute {
		t.Errorf("Cache = %+v", cfg.Cache)
	}
}

func TestBindReportsAllErrors(t *testing.T) {
	src := Map{
		"APP_DEBUG":   "maybe",
		"PORT":        "70000", // overflows uint16
		"DB_MAX_OPEN": "lots",
		"DB_TIMEOUT":  "5",
		"PORTS":       "1,x",
	}
	err := Bind(src, &appConfig{})
	if err == nil {
		t.Fatal("expected errors")
	}

	var fieldErrs []*FieldError
	for _, e := range err.(interface{ Unwrap() []error }).Unwrap() {
		var fe *FieldError
		if errors.As(e, &fe) {
			fieldErrs = append(fieldErrs, fe)
		}
	}
	gotKeys := map[string]bool{}
	for _, fe := range fieldErrs {
		gotKeys[fe.Key] = true
	}
	for _, k := range []string{"APP_DEBUG", "PORT", "DB_MAX_OPEN", "DB_TIMEOUT", "PORTS", "DB_URL"} {
		if !gotKeys[k] {
			t.Errorf("missing error for %s; got: %v", k, err)
		}
	}
	if !errors.Is(err, ErrMissing) {
		t.Errorf("errors.Is(err, ErrMissing) = false; err: %v", err)
	}
	msg := err.Error()
	for _, want := range []string{
		`config: APP_DEBUG (appConfig.Debug): invalid boolean "maybe" (use true or false)`,
		`config: DB_URL (appConfig.DB.URL): required but not set`,
		`config: DB_TIMEOUT (appConfig.DB.Timeout): invalid duration "5"`,
		`config: PORTS (appConfig.Ports): item 1: invalid integer "x"`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q\ngot:\n%s", want, msg)
		}
	}
}

func TestBindValidate(t *testing.T) {
	err := Bind(Map{"DB_URL": "x", "DB_DRIVER": "oracle"}, &appConfig{})
	if err == nil || !strings.Contains(err.Error(), `config: appConfig.DB: unknown driver "oracle"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestBindRejectsNonStructPointer(t *testing.T) {
	for _, dst := range []any{nil, appConfig{}, new(int), (*appConfig)(nil)} {
		if err := Bind(Map{}, dst); err == nil {
			t.Errorf("Bind(%T) succeeded, want error", dst)
		}
	}
}

func TestBindUnsupportedType(t *testing.T) {
	var cfg struct {
		M map[string]string `env:"M"`
	}
	err := Bind(Map{"M": "x"}, &cfg)
	if err == nil || !strings.Contains(err.Error(), "unsupported field type map[string]string") {
		t.Fatalf("err = %v", err)
	}
}

func TestLayers(t *testing.T) {
	src := Layers(Map{"A": "1"}, nil, Map{"A": "2", "B": "2"})
	if v, _ := src.Lookup("A"); v != "1" {
		t.Errorf("A = %q, want first layer", v)
	}
	if v, _ := src.Lookup("B"); v != "2" {
		t.Errorf("B = %q", v)
	}
	if _, ok := src.Lookup("C"); ok {
		t.Error("C found")
	}
}

func TestEnvSource(t *testing.T) {
	t.Setenv("ANETOS_TEST_ENV_SOURCE", "yes")
	if v, ok := Env().Lookup("ANETOS_TEST_ENV_SOURCE"); !ok || v != "yes" {
		t.Errorf("Env lookup = %q, %v", v, ok)
	}
}

func TestLoadLayering(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".env", "APP_ENV=testing\nA=base\nB=base\nC=base\nURL=http://${HOST}:8080\n")
	write(t, dir, ".env.testing", "B=testing\nC=testing\nREF=${A}\n")

	src, err := Load(LoadOptions{Dir: dir, OSEnv: Map{"C": "os", "HOST": "db"}})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for k, want := range map[string]string{"A": "base", "B": "testing", "C": "os", "URL": "http://db:8080", "REF": "base"} {
		if got, _ := src.Lookup(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func TestLoadEnvironmentFromOSAndOption(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".env", "APP_ENV=development\nX=base\n")
	write(t, dir, ".env.development", "X=dev\n")
	write(t, dir, ".env.production", "X=prod\n")

	src, _ := Load(LoadOptions{Dir: dir, OSEnv: Map{"APP_ENV": "production"}})
	if got, _ := src.Lookup("X"); got != "prod" {
		t.Errorf("OS APP_ENV: X = %q, want prod", got)
	}
	src, _ = Load(LoadOptions{Dir: dir, OSEnv: Map{}, Environment: "development"})
	if got, _ := src.Lookup("X"); got != "dev" {
		t.Errorf("option: X = %q, want dev", got)
	}
}

func TestLoadMissingFilesIsFine(t *testing.T) {
	src, err := Load(LoadOptions{Dir: t.TempDir(), OSEnv: Map{"K": "v"}})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, _ := src.Lookup("K"); got != "v" {
		t.Errorf("K = %q", got)
	}
}

func TestLoadParseErrorNamesFile(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".env", "OK=1\nBROKEN\n")
	_, err := Load(LoadOptions{Dir: dir, OSEnv: Map{}})
	if err == nil || !strings.Contains(err.Error(), ".env: line 2:") {
		t.Fatalf("err = %v", err)
	}
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// --- regression tests from the F2–F4 code review ---

type tlsSection struct {
	Cert string `env:"CERT,required"`
	Port int    `env:"PORT" default:"443"`
}

func (s tlsSection) Validate() error {
	if s.Port == 0 {
		return errors.New("port required")
	}
	return nil
}

func TestOptionalPointerSectionStaysNil(t *testing.T) {
	type cfg struct {
		TLS *tlsSection `prefix:"TLS_"`
	}
	got, err := Get[cfg](Map{})
	if err != nil {
		t.Fatalf("absent optional section: %v", err)
	}
	if got.TLS != nil {
		t.Errorf("TLS = %+v, want nil", got.TLS)
	}

	// Configured: allocated, defaults applied, required keys enforced.
	got, err = Get[cfg](Map{"TLS_CERT": "cert.pem"})
	if err != nil || got.TLS == nil || got.TLS.Port != 443 {
		t.Fatalf("got %+v, err %v", got.TLS, err)
	}
	_, err = Get[cfg](Map{"TLS_PORT": "8443"})
	if err == nil || !strings.Contains(err.Error(), "TLS_CERT") {
		t.Errorf("partially configured section: err = %v", err)
	}
}

type node struct {
	Name string `env:"NAME"`
	Next *node
}

func TestRecursiveTypeTerminates(t *testing.T) {
	got, err := Get[node](Map{"NAME": "root"})
	if err != nil || got.Name != "root" || got.Next != nil {
		t.Fatalf("got %+v, err %v", got, err)
	}
}

type inner struct {
	X int `env:"X"`
}

func TestEmbeddedUnexportedStruct(t *testing.T) {
	var cfg struct {
		inner
	}
	if err := Bind(Map{"X": "5"}, &cfg); err != nil || cfg.X != 5 {
		t.Fatalf("X = %d, err = %v", cfg.X, err)
	}

	var withPtr struct {
		*inner
	}
	if err := Bind(Map{"X": "5"}, &withPtr); err == nil || !strings.Contains(err.Error(), "embedded pointer to an unexported type") {
		t.Errorf("err = %v", err)
	}
}

func TestBadEnvTags(t *testing.T) {
	var cfg struct {
		A string `env:"A,required,trim"`
		B string `env:""`
	}
	err := Bind(Map{"A": "x"}, &cfg)
	for _, want := range []string{`unknown env tag option "trim"`, "env tag has an empty name"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to contain %q", err, want)
		}
	}
}
