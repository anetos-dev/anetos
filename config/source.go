// SPDX-License-Identifier: Apache-2.0

package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Source provides raw configuration values by key.
//
// Implementations must be safe for concurrent use once constructed.
type Source interface {
	// Lookup returns the value stored under key and whether it was present.
	Lookup(key string) (string, bool)
}

// Map is a [Source] backed by a map. The zero value is an empty source.
type Map map[string]string

// Lookup implements [Source].
func (m Map) Lookup(key string) (string, bool) {
	v, ok := m[key]
	return v, ok
}

// Env returns a [Source] that reads the process environment.
func Env() Source { return envSource{} }

type envSource struct{}

func (envSource) Lookup(key string) (string, bool) { return os.LookupEnv(key) }

// Layers returns a [Source] that consults sources in order and returns the
// first value found. Put the highest-priority source first.
func Layers(sources ...Source) Source { return layered(sources) }

type layered []Source

func (l layered) Lookup(key string) (string, bool) {
	for _, s := range l {
		if s == nil {
			continue
		}
		if v, ok := s.Lookup(key); ok {
			return v, true
		}
	}
	return "", false
}

// LoadOptions controls [Load].
type LoadOptions struct {
	// Dir is the directory containing the .env files. Default ".".
	Dir string

	// Environment selects the environment-specific file (.env.<Environment>).
	// If empty, APP_ENV is read from the process environment, then from .env.
	Environment string

	// OSEnv is the highest-priority source. Default [Env]; tests can pass a
	// [Map] to isolate themselves from the real environment.
	OSEnv Source
}

// Load builds the standard layered configuration source. From highest to
// lowest priority:
//
//  1. the process environment (opts.OSEnv)
//  2. .env.<APP_ENV>, if APP_ENV is set and the file exists
//  3. .env, if it exists
//
// Missing files are not an error, so production deployments can rely on the
// process environment alone. A file that exists but cannot be parsed is an
// error that names the file and line.
//
// ${NAME} references inside the files resolve with the same priority, so a
// reference sees the value the application sees.
func Load(opts LoadOptions) (Source, error) {
	if opts.Dir == "" {
		opts.Dir = "."
	}
	if opts.OSEnv == nil {
		opts.OSEnv = Env()
	}

	base, err := readDotenvFile(filepath.Join(opts.Dir, ".env"), opts.OSEnv, nil)
	if err != nil {
		return nil, err
	}

	appEnv := opts.Environment
	if appEnv == "" {
		appEnv, _ = Layers(opts.OSEnv, base).Lookup("APP_ENV")
	}

	var specific Map
	if appEnv != "" {
		specific, err = readDotenvFile(filepath.Join(opts.Dir, ".env."+appEnv), opts.OSEnv, base)
		if err != nil {
			return nil, err
		}
	}

	return Layers(opts.OSEnv, specific, base), nil
}

// readDotenvFile parses path, resolving ${NAME} references from over (the
// process environment), then the file itself, then under (lower layers).
func readDotenvFile(path string, over, under Source) (Map, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	m, err := parseDotenv(string(data), over, under)
	if err != nil {
		return nil, fmt.Errorf("config: %s: %w", path, err)
	}
	return m, nil
}
