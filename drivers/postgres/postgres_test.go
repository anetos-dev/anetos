// SPDX-License-Identifier: Apache-2.0

package postgres_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/dbtest"
	"anetos.dev/anetos/drivers/postgres"
)

// TestConformance runs against the database in ANETOS_TEST_POSTGRES_URL,
// e.g. postgres://postgres@127.0.0.1:5432/anetos_test.
func TestConformance(t *testing.T) {
	url := os.Getenv("ANETOS_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("ANETOS_TEST_POSTGRES_URL not set")
	}
	d, err := db.Open(postgres.Driver(), db.Config{Driver: "postgres", URL: url, MaxOpenConns: 10, MaxIdleConns: 10})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := d.Ping(t.Context()); err != nil {
		t.Fatal(err)
	}
	dbtest.Run(t, d)
}

// TestApp tests an app on the database with anetostest.
func TestApp(t *testing.T) {
	url := os.Getenv("ANETOS_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("ANETOS_TEST_POSTGRES_URL not set")
	}
	dbtest.RunApp(t, postgres.Driver(), map[string]string{"DB_DRIVER": "postgres", "DB_URL": url})
}

// TestLocalTimeZone checks that a session time zone set in DB_URL
// stops the app at boot.
func TestLocalTimeZone(t *testing.T) {
	url := os.Getenv("ANETOS_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("ANETOS_TEST_POSTGRES_URL not set")
	}
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	dbtest.RunLocalTimeZone(t, postgres.Driver(), db.Config{Driver: "postgres", URL: url + sep + "timezone=Asia/Dhaka"})
}

func TestDSN(t *testing.T) {
	cases := []struct {
		cfg  db.Config
		want string
	}{
		{db.Config{Host: "db", Name: "app", User: "u", Password: "p@ss/word"}, "postgres://u:p%40ss%2Fword@db:5432/app?sslmode=verify-full&timezone=UTC"},
		{db.Config{Host: "::1", Port: 6543, Name: "app"}, "postgres://[::1]:6543/app?sslmode=disable&timezone=UTC"},
		{db.Config{Host: "db", Name: "app", User: "u", TLS: "none"}, "postgres://u@db:5432/app?sslmode=disable&timezone=UTC"},
		{db.Config{Host: "db", Name: "app", TLS: "skip-verify"}, "postgres://db:5432/app?sslmode=require&timezone=UTC"},
		{db.Config{Host: "db", Name: "app", TLSCA: "/etc/ca.pem"}, "postgres://db:5432/app?sslmode=verify-full&sslrootcert=%2Fetc%2Fca.pem&timezone=UTC"},
		{db.Config{Host: "localhost", Name: "app"}, "postgres://localhost:5432/app?sslmode=disable&timezone=UTC"},
		{db.Config{Host: "127.0.0.1", Name: "app", TLSCA: "/etc/ca.pem"}, "postgres://127.0.0.1:5432/app?sslmode=verify-full&sslrootcert=%2Fetc%2Fca.pem&timezone=UTC"}, // a tunnel
		{db.Config{URL: "postgres://x/y?sslmode=require"}, "postgres://x/y?sslmode=require&timezone=UTC"},
		{db.Config{URL: "postgres://x/y?TimeZone=Asia/Dhaka"}, "postgres://x/y?TimeZone=Asia/Dhaka"},
		{db.Config{URL: "host=x dbname=y"}, "host=x dbname=y timezone=UTC"},
		{db.Config{URL: "host=x timezone=UTC"}, "host=x timezone=UTC"},
		{db.Config{Host: "/var/run/postgresql", Name: "my db", User: "u"}, "postgres://u@/my%20db?host=%2Fvar%2Frun%2Fpostgresql&port=5432&sslmode=disable&timezone=UTC"},
	}
	for _, c := range cases {
		if got := postgres.DSN(c.cfg); got != c.want {
			t.Errorf("DSN = %s, want %s", got, c.want)
		}
	}
}

func TestInspectURL(t *testing.T) {
	inspect := postgres.Driver().InspectURL
	for url, want := range map[string]string{
		"postgres://u:p@db.example.com/app":                           "db.example.com none", // prefer, the default, falls back to plain text
		"postgres://u:p@db.example.com/app?sslmode=disable":           "db.example.com none",
		"postgres://u:p@db.example.com/app?sslmode=allow":             "db.example.com none",
		"postgres://u:p@db.example.com/app?sslmode=require":           "db.example.com skip-verify",
		"postgres://u:p@db.example.com/app?sslmode=verify-full":       "db.example.com verify",
		"host=10.0.0.5 user=u dbname=app sslmode=verify-full":         "10.0.0.5 verify",
		"postgres://u@%2Fvar%2Frun%2Fpostgresql/app?sslmode=disable":  "/var/run/postgresql none",
		"postgres://u:p@db.example.com/app?sslmode=verify-ca":         "db.example.com skip-verify", // public CAs, the name unchecked
		"postgres://u:p@localhost,db.example.com/app?sslmode=disable": "db.example.com none",
		"postgres://u:p@localhost,127.0.0.1/app?sslmode=disable":      "localhost none",
	} {
		host, mode, err := inspect(url)
		if got := host + " " + mode; err != nil || got != want {
			t.Errorf("%s: %q, %v; want %q", url, got, err, want)
		}
	}
	// verify-ca with the server's own authority verifies.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"}, NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"verify-ca", "require"} { // require with a CA is verify-ca
		if host, got, err := inspect("postgres://db.example.com/app?sslmode=" + mode + "&sslrootcert=" + ca); err != nil || got != "verify" || host != "db.example.com" {
			t.Errorf("%s with sslrootcert: %s %s %v", mode, host, got, err)
		}
	}
	if _, _, err := inspect("postgres://db.example.com/app?sslmode=verify-full&sslrootcert=/nonexistent/ca.pem"); err == nil {
		t.Error("a missing sslrootcert: no error")
	}
}
