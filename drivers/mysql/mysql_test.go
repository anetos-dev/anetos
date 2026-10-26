// SPDX-License-Identifier: Apache-2.0

package mysql_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"

	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/dbtest"
	"anetos.dev/anetos/drivers/mysql"
)

// TestConformance runs against the database in ANETOS_TEST_MYSQL_URL, e.g.
// anetos:secret@tcp(127.0.0.1:3306)/anetos_test.
func TestConformance(t *testing.T) {
	url := os.Getenv("ANETOS_TEST_MYSQL_URL")
	if url == "" {
		t.Skip("ANETOS_TEST_MYSQL_URL not set")
	}
	d, err := db.Open(mysql.Driver(), db.Config{Driver: "mysql", URL: url, MaxOpenConns: 10, MaxIdleConns: 10})
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
	url := os.Getenv("ANETOS_TEST_MYSQL_URL")
	if url == "" {
		t.Skip("ANETOS_TEST_MYSQL_URL not set")
	}
	dbtest.RunApp(t, mysql.Driver(), map[string]string{"DB_DRIVER": "mysql", "DB_URL": url})
}

// TestLocalTimeZone checks that a session time zone set in DB_URL
// stops the app at boot.
func TestLocalTimeZone(t *testing.T) {
	url := os.Getenv("ANETOS_TEST_MYSQL_URL")
	if url == "" {
		t.Skip("ANETOS_TEST_MYSQL_URL not set")
	}
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	dbtest.RunLocalTimeZone(t, mysql.Driver(), db.Config{Driver: "mysql", URL: url + sep + "time_zone=%27%2B06:00%27"})

	// The server's own zone (SYSTEM) is accepted only if it is UTC.
	d, err := db.Open(mysql.Driver(), db.Config{Driver: "mysql", URL: url + sep + "time_zone=%27SYSTEM%27"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var utc bool
	if err := d.SQL().QueryRowContext(t.Context(), `SELECT FROM_UNIXTIME(1768478400) = '2026-01-15 12:00:00' AND FROM_UNIXTIME(1784116800) = '2026-07-15 12:00:00'`).Scan(&utc); err != nil {
		t.Fatal(err)
	}
	err = d.CheckTimeZone(t.Context())
	if utc != (err == nil) || (err != nil && !strings.Contains(err.Error(), "the server's")) {
		t.Errorf("SYSTEM zone (UTC: %v): %v", utc, err)
	}
}

func TestDSN(t *testing.T) {
	got, err := mysql.DSN(db.Config{Host: "db", Name: "app", User: "u", Password: "p@ss"})
	if err != nil || !strings.HasPrefix(got, "u:p@ss@tcp(db:3306)/app?") || !strings.Contains(got, "parseTime=true") || !strings.Contains(got, "clientFoundRows=true") {
		t.Errorf("DSN = %s, %v", got, err)
	}
	if !strings.Contains(got, "tls=true") {
		t.Errorf("a remote host without TLS: %s", got)
	}
	for tlsMode, want := range map[string]string{"none": "tls=false", "skip-verify": "tls=skip-verify", "verify": "tls=true"} {
		if got, _ := mysql.DSN(db.Config{Host: "db", TLS: tlsMode}); !strings.Contains(got, want) {
			t.Errorf("DB_TLS=%s: %s", tlsMode, got)
		}
	}
	if got, _ := mysql.DSN(db.Config{Host: "127.0.0.1"}); !strings.Contains(got, "tls=false") {
		t.Errorf("a local host: %s", got)
	}
	if _, err := mysql.DSN(db.Config{Host: "db", TLSCA: "/nonexistent.pem"}); err == nil {
		t.Error("a missing DB_TLS_CA accepted")
	}
	got, err = mysql.DSN(db.Config{URL: "u:p@tcp(h:1)/x?loc=Local"})
	if err != nil || !strings.Contains(got, "parseTime=true") || strings.Contains(got, "Local") {
		t.Errorf("DSN from URL = %s, %v", got, err)
	}
	if _, err := mysql.DSN(db.Config{URL: "not a dsn"}); err == nil {
		t.Error("bad DSN accepted")
	}
}

func TestInspectURL(t *testing.T) {
	inspect := mysql.Driver().InspectURL
	for url, want := range map[string]string{
		"u:p@tcp(db.example.com:3306)/app":                                        "db.example.com none",
		"u:p@tcp(db.example.com:3306)/app?tls=false":                              "db.example.com none",
		"u:p@tcp(db.example.com:3306)/app?tls=preferred":                          "db.example.com none",
		"u:p@tcp(db.example.com:3306)/app?tls=skip-verify":                        "db.example.com skip-verify",
		"u:p@tcp(db.example.com:3306)/app?tls=true":                               "db.example.com verify",
		"u:p@tcp(db.example.com:3306)/app?tls=true&allowFallbackToPlaintext=true": "db.example.com none",
		"u:p@tcp([2001:db8::1]:3306)/app?tls=true":                                "2001:db8::1 verify",
		"u:p@unix(/run/mysqld/mysqld.sock)/app":                                   "/run/mysqld/mysqld.sock none",
	} {
		host, mode, err := inspect(url)
		if got := host + " " + mode; err != nil || got != want {
			t.Errorf("%s: %q, %v; want %q", url, got, err, want)
		}
	}
	if _, _, err := inspect("not a dsn"); err == nil {
		t.Error("a bad DSN: no error")
	}
}

// DB_TLS_CA as PEM text registers its own TLS configuration; text without
// a certificate fails.
func TestInlineCA(t *testing.T) {
	ca := testCA(t)
	for _, text := range []string{ca, strings.ReplaceAll(ca, "\n", `\n`)} { // as given, and with "\n" written out
		got, err := mysql.DSN(db.Config{Host: "db", TLSCA: text})
		if err != nil || !strings.Contains(got, "tls=anetos-") {
			t.Errorf("DSN = %s, %v", got, err)
		}
	}
	if _, err := mysql.DSN(db.Config{Host: "db", TLSCA: "-----BEGIN CERTIFICATE-----\nnope\n-----END CERTIFICATE-----"}); err == nil || !strings.Contains(err.Error(), "PEM text has no certificates") {
		t.Errorf("bad PEM text: %v", err)
	}
}

// testCA is a self-signed CA's certificate, as PEM.
func testCA(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"}, NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}
