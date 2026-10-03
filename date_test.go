// SPDX-License-Identifier: Apache-2.0

package anetos_test

import (
	"cmp"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
)

func TestDate(t *testing.T) {
	d, err := anetos.ParseDate("2026-03-15")
	if err != nil || d != (anetos.Date{Year: 2026, Month: time.March, Day: 15}) || d.String() != "2026-03-15" {
		t.Fatalf("ParseDate = %v, %v", d, err)
	}
	for _, bad := range []string{"2026-02-30", "15/03/2026", "2026-3-5", "2026-03-15T00:00:00Z"} {
		if _, err := anetos.ParseDate(bad); err == nil {
			t.Errorf("ParseDate(%q) = nil", bad)
		}
	}
	if z, err := anetos.ParseDate(""); err != nil || !z.IsZero() || z.String() != "" {
		t.Errorf("ParseDate(\"\") = %v, %v", z, err)
	}

	// Normalization, arithmetic and order.
	if got := anetos.NewDate(2026, time.October, 32); got != anetos.NewDate(2026, time.November, 1) {
		t.Errorf("NewDate normalizes to %v", got)
	}
	if !d.IsValid() || (anetos.Date{Year: 2026, Month: 2, Day: 30}).IsValid() || (anetos.Date{}).IsValid() {
		t.Error("IsValid")
	}
	if d.AddDays(17) != anetos.NewDate(2026, time.April, 1) || d.AddDays(-15) != anetos.NewDate(2026, time.February, 28) {
		t.Errorf("AddDays: %v, %v", d.AddDays(17), d.AddDays(-15))
	}
	if d.AddDate(0, 1, 0) != anetos.NewDate(2026, time.April, 15) || anetos.NewDate(2028, 1, 31).AddDate(0, 1, 0) != anetos.NewDate(2028, 3, 2) {
		t.Error("AddDate")
	}
	if n := anetos.NewDate(2027, 3, 15).DaysSince(d); n != 365 {
		t.Errorf("DaysSince = %d", n)
	}
	if d.Weekday() != time.Sunday {
		t.Errorf("Weekday = %v", d.Weekday())
	}
	later := d.AddDays(1)
	if !d.Before(later) || !later.After(d) || d.Compare(anetos.NewDate(2026, 3, 15)) != 0 || d.Compare(anetos.NewDate(2025, 12, 31)) != 1 {
		t.Error("Compare")
	}
	// Out-of-range fields compare as the day they stand for.
	if c := (anetos.Date{Year: 2026, Month: 10, Day: 32}).Compare(anetos.NewDate(2026, 11, 1)); c != 0 {
		t.Errorf("October 32 vs November 1: %d", c)
	}
	if n := anetos.NewDate(2026, 1, 1).DaysSince(anetos.NewDate(1700, 1, 1)); n != 119069 {
		t.Errorf("DaysSince over centuries = %d", n)
	}
	if (anetos.Date{Year: 10000, Month: 1, Day: 1}).IsValid() || !anetos.NewDate(1, 1, 1).IsValid() {
		t.Error("IsValid range")
	}

	// A time's date is its day in its own zone.
	dhaka := time.FixedZone("BST", 6*3600)
	at := time.Date(2026, 3, 16, 1, 0, 0, 0, dhaka) // 2026-03-15 19:00 UTC
	if anetos.DateOf(at) != anetos.NewDate(2026, 3, 16) || anetos.DateOf(at.UTC()) != d {
		t.Errorf("DateOf: %v, %v", anetos.DateOf(at), anetos.DateOf(at.UTC()))
	}
	if !d.In(dhaka).Equal(time.Date(2026, 3, 15, 0, 0, 0, 0, dhaka)) {
		t.Errorf("In = %v", d.In(dhaka))
	}
}

func TestDateEncoding(t *testing.T) {
	d := anetos.NewDate(2026, 10, 3)
	type doc struct {
		Due  anetos.Date  `json:"due"`
		Done *anetos.Date `json:"done"`
		None anetos.Date  `json:"none"`
	}
	b, err := json.Marshal(doc{Due: d})
	if err != nil || string(b) != `{"due":"2026-10-03","done":null,"none":""}` {
		t.Fatalf("Marshal = %s, %v", b, err)
	}
	var back doc
	if err := json.Unmarshal(b, &back); err != nil || back.Due != d || back.Done != nil || !back.None.IsZero() {
		t.Errorf("Unmarshal = %+v, %v", back, err)
	}
	if err := json.Unmarshal([]byte(`{"due":"tomorrow"}`), &back); err == nil {
		t.Error("a bad date unmarshaled")
	}

	// Database values: text out; text, bytes or a time in.
	if v, err := d.Value(); err != nil || v != "2026-10-03" {
		t.Errorf("Value = %v, %v", v, err)
	}
	if v, err := (anetos.Date{}).Value(); err != nil || v != nil {
		t.Errorf("zero Value = %v, %v", v, err)
	}
	for _, src := range []any{"2026-10-03", []byte("2026-10-03 00:00:00"), time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)} {
		var got anetos.Date
		if err := got.Scan(src); err != nil || got != d {
			t.Errorf("Scan(%#v) = %v, %v", src, got, err)
		}
	}
	got := d
	if err := got.Scan(nil); err != nil || !got.IsZero() {
		t.Errorf("Scan(nil) = %v, %v", got, err)
	}
	got = d
	if err := got.Scan(time.Time{}); err != nil || !got.IsZero() {
		t.Errorf("Scan of the zero time (MySQL's 0000-00-00) = %v, %v", got, err)
	}
	for _, bad := range []any{42, "2026-10-03junk", "2026-10"} {
		if err := got.Scan(bad); err == nil {
			t.Errorf("Scan(%#v) = nil", bad)
		}
	}

	// A date that isn't valid isn't written as another day.
	for _, bad := range []anetos.Date{{Year: 2026, Month: 2, Day: 30}, {Year: 2026}} {
		if _, err := bad.Value(); err == nil {
			t.Errorf("%#v.Value() = nil", bad)
		}
		if _, err := json.Marshal(bad); err == nil {
			t.Errorf("json.Marshal(%#v) = nil", bad)
		}
	}
}

func TestTimeZone(t *testing.T) {
	// The test binary's local zone is APP_TIMEZONE's (UTC when unset).
	process := cmp.Or(os.Getenv("APP_TIMEZONE"), "UTC")
	if time.Local.String() != process {
		t.Errorf("time.Local = %v, want %s", time.Local, process)
	}
	utc := newApp(t, config.Map{"APP_TIMEZONE": "UTC"})
	if utc.Location().String() != "UTC" || anetos.Location(utc.Context(context.Background())).String() != "UTC" {
		t.Errorf("zone = %v", utc.Location())
	}
	// A frozen clock in another zone is shown in the app's.
	utc.SetClock(func() time.Time { return time.Date(2026, 3, 16, 1, 0, 0, 0, time.FixedZone("BST", 6*3600)) })
	if now := anetos.Now(utc.Context(context.Background())); now.Location().String() != "UTC" || now.Hour() != 19 {
		t.Errorf("Now = %v", now)
	}

	app := newApp(t, config.Map{"APP_TIMEZONE": "Asia/Dhaka"})
	ctx := app.Context(context.Background())
	if app.Location().String() != "Asia/Dhaka" || anetos.Location(ctx) != app.Location() {
		t.Fatalf("zone = %v", app.Location())
	}
	// In a test binary, apps don't change the process's zone; Now is in
	// the app's.
	if time.Local.String() != process {
		t.Errorf("time.Local changed to %v", time.Local)
	}
	at := time.Date(2026, 3, 15, 20, 0, 0, 0, time.UTC)
	app.SetClock(func() time.Time { return at })
	if now := anetos.Now(ctx); !now.Equal(at) || now.Location() != app.Location() || now.Hour() != 2 {
		t.Errorf("Now = %v", now)
	}
	if got := anetos.Today(ctx); got != anetos.NewDate(2026, 3, 16) {
		t.Errorf("Today = %v (in Dhaka it's the 16th)", got)
	}
	if anetos.Location(context.Background()) != time.Local {
		t.Error("Location without an app isn't time.Local")
	}

	for _, bad := range []string{"Mars/Olympus", "Local"} {
		if _, err := anetos.New(quiet(), anetos.WithSource(config.Map{"APP_TIMEZONE": bad})); err == nil {
			t.Errorf("APP_TIMEZONE=%s: no error", bad)
		}
	}
	if err := (anetos.AppConfig{Env: anetos.Production, ShutdownTimeout: time.Second}).Validate(); err != nil {
		t.Errorf("an empty TimeZone (UTC) is invalid: %v", err)
	}
}

// TestProcessZone runs a program outside the test binary: the first app
// sets the process's local zone from its .env file, a second app doesn't
// change it.
func TestProcessZone(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a program")
	}
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	sum, err := os.ReadFile("go.sum")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"go.mod": "module zoneprobe\n\ngo 1.26\n\nrequire anetos.dev/anetos v0.0.0\n\nreplace anetos.dev/anetos => " + root + "\n",
		"go.sum": string(sum),
		".env":   "APP_ENV=production\nAPP_TIMEZONE=Asia/Dhaka\n",
		"main.go": `package main

import (
	"fmt"
	"io"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
)

func main() {
	before := time.Local.String()
	a, err := anetos.New(anetos.WithLogOutput(io.Discard))
	if err != nil {
		panic(err)
	}
	first := time.Local.String()
	b, err := anetos.New(anetos.WithLogOutput(io.Discard), anetos.WithSource(config.Map{"APP_TIMEZONE": "Europe/Paris"}))
	if err != nil {
		panic(err)
	}
	fmt.Println(before, first, time.Local.String(), a.Location(), b.Location())
}
`,
	}
	for name, content := range files {
		if err := os.WriteFile(dir+"/"+name, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", "run", "-mod=mod", ".")
	cmd.Dir = dir
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "APP_") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go run: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "UTC Asia/Dhaka Asia/Dhaka Asia/Dhaka Europe/Paris" {
		t.Errorf("zones (before New, after the first app, after the second, the apps') = %s", got)
	}
}
