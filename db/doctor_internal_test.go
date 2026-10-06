// SPDX-License-Identifier: Apache-2.0

package db

import (
	"errors"
	"strings"
	"testing"

	"anetos.dev/anetos"
)

func TestDoctorChecks(t *testing.T) {
	yes := true
	inspect := Driver{InspectURL: func(url string) (string, string, error) {
		if url == "bad" {
			return "", "", errors.New("bad DSN")
		}
		host, mode, _ := strings.Cut(url, "|")
		return host, mode, nil
	}}
	tests := []struct {
		cfg  Config
		d    Driver
		env  anetos.Environment
		want string // "" for no finding
	}{
		{Config{Connection: "postgres", Host: "db.example.com"}, Driver{}, anetos.Production, ""}, // verify by default
		{Config{Connection: "postgres", Host: "db.example.com", TLS: TLSNone}, Driver{}, anetos.Production, "warning:DB_TLS=none for db.example.com: connections may go in plain text"},
		{Config{Connection: "mysql", Host: "10.0.0.5", TLS: TLSSkipVerify}, Driver{}, anetos.Staging, "warning:DB_TLS=skip-verify for 10.0.0.5: connections are encrypted, but"},
		{Config{Connection: "postgres", Host: "127.0.0.1", TLS: TLSNone}, Driver{}, anetos.Production, ""},
		{Config{Connection: "postgres", Host: "db.example.com", TLS: TLSNone}, Driver{}, anetos.Development, ""},
		{Config{Connection: "sqlite", LogQueries: &yes}, Driver{}, anetos.Production, "warning:DB_LOG_QUERIES=true in production"},
		{Config{Connection: "sqlite", TLS: TLSNone}, Driver{}, anetos.Production, ""},
		{Config{Connection: "postgres", Host: "localhost", LogQueries: &yes}, Driver{}, anetos.Production, "warning:DB_LOG_QUERIES=true in production"},
		{Config{Connection: "postgres", URL: "db.example.com|none"}, inspect, anetos.Production, "warning:DB_URL for db.example.com: connections may go in plain text"},
		{Config{Connection: "postgres", URL: "db.example.com|verify"}, inspect, anetos.Production, ""},
		{Config{Connection: "postgres", URL: "/var/run/postgresql|none"}, inspect, anetos.Production, ""},
		{Config{Connection: "postgres", URL: "bad"}, inspect, anetos.Production, "warning:DB_URL can't be read to check its TLS settings: bad DSN"},
		{Config{Connection: "postgres", URL: "db.example.com|none"}, Driver{}, anetos.Production, ""}, // the driver can't tell
	}
	for _, tt := range tests {
		found := checks(tt.cfg, tt.d, tt.env)
		var got string
		if len(found) > 0 {
			got = found[0].Severity.String() + ":" + found[0].Message
		}
		if len(found) > 1 || tt.want == "" && got != "" || !strings.HasPrefix(got, tt.want) {
			t.Errorf("%v in %s: got %v, want %q", tt.cfg, tt.env, found, tt.want)
		}
	}
}
