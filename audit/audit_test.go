// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/cmd"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/encryption"
)

func TestMaskIP(t *testing.T) {
	for _, tt := range []struct{ ip, mode, want string }{
		{"203.0.113.77", "none", ""},
		{"203.0.113.77", "masked", "203.0.113.0"},
		{"203.0.113.77", "full", "203.0.113.77"},
		{"2001:db8:1234:5678::1", "masked", "2001:db8:1234::"},
		{"::ffff:203.0.113.77", "masked", "203.0.113.0"},
		{"", "full", ""},
		{"not an ip", "full", ""},
	} {
		if got := maskIP(tt.ip, tt.mode); got != tt.want {
			t.Errorf("maskIP(%q, %s) = %q, want %q", tt.ip, tt.mode, got, tt.want)
		}
	}
}

func TestConfig(t *testing.T) {
	cfg, err := config.Get[Config](config.Map{})
	if err != nil || cfg.IP != "none" || cfg.BulkMaxValues != 10000 || cfg.RetentionDays != 0 {
		t.Errorf("defaults: %+v, %v", cfg, err)
	}
	for _, bad := range []config.Map{{"AUDIT_IP": "some"}, {"AUDIT_BULK_MAX_VALUES": "-1"}, {"AUDIT_RETENTION_DAYS": "-1"}} {
		if _, err := config.Get[Config](bad); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}

func TestSame(t *testing.T) {
	now := time.Now().UTC()
	for _, tt := range []struct {
		a, b any
		want bool
	}{
		{now, now.In(time.FixedZone("x", 3600)), true},
		{now, now.Add(time.Microsecond), false},
		{[]byte("a"), []byte("a"), true},
		{json.RawMessage(`{"a": 1}`), json.RawMessage(`{"a":1}`), true},
		{json.RawMessage(`{"a":1}`), json.RawMessage(`{"a":2}`), false},
		{int64(1), int64(1), true},
		{nil, "", false},
		{nil, nil, true},
	} {
		if got := same(tt.a, tt.b); got != tt.want {
			t.Errorf("same(%v, %v) = %v", tt.a, tt.b, got)
		}
	}
}

func TestTrackingValues(t *testing.T) {
	tr := &tracking{hidden: map[string]bool{"score": true}, masked: map[string]bool{"api_token": true}}
	got := tr.values(map[string]any{"title": "a", "score": 1, "api_token": "x", "nil_token": nil})
	if got["title"] != "a" || got["api_token"] != Redacted || len(got) != 3 {
		t.Errorf("values = %v", got)
	}
	c := tr.diff(db.Values{"title": "a", "score": 1, "api_token": "x", "updated_at": 1},
		db.Values{"title": "b", "score": 2, "api_token": "y", "updated_at": 2})
	if c.Old["title"] != "a" || c.New["title"] != "b" || c.New["api_token"] != Redacted || len(c.New) != 2 {
		t.Errorf("diff = %+v", c)
	}
	if f := c.Fields(); len(f) != 2 || f[0] != "api_token" {
		t.Errorf("Fields = %v", f)
	}
}

func TestActors(t *testing.T) {
	if a, ok := parseActor("user:42"); !ok || a != User("42") || a.String() != "user:42" {
		t.Errorf("parseActor(user:42) = %v, %v", a, ok)
	}
	if a, ok := parseActor("system"); !ok || a != System || a.String() != "system" {
		t.Errorf("parseActor(system) = %v, %v", a, ok)
	}
	if _, ok := parseActor("Bad Type:1"); ok {
		t.Error("parseActor accepted a bad type")
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("WithActor didn't panic on a bad actor")
			}
		}()
		WithActor(context.Background(), Actor{Type: "Not OK"})
	}()

	ctx := context.Background()
	if a, err := ActorOf(ctx); err != nil || a != System {
		t.Errorf("ActorOf(background) = %v, %v", a, err)
	}
	if a, _ := ActorOf(context.WithValue(ctx, carriedKey{}, User("7"))); a != User("7") {
		t.Errorf("carried actor = %v", a)
	}
	svc := Actor{Type: "service", ID: "stripe"}
	if a, _ := ActorOf(WithActor(ctx, svc)); a != svc {
		t.Errorf("WithActor = %v", a)
	}

	// The signed-in (here: acting) user, and an error loading them.
	cfg, err := auth.LoadConfig(config.Map{})
	if err != nil {
		t.Fatal(err)
	}
	key, err := encryption.ParseKey(encryption.GenerateKey())
	if err != nil {
		t.Fatal(err)
	}
	enc, err := encryption.NewEncrypter(key)
	if err != nil {
		t.Fatal(err)
	}
	errDown := errors.New("database down")
	a, err := auth.NewWithConfig(cfg, auth.Users[user]{
		ByID: func(_ context.Context, id string) (user, error) {
			if id == "down" {
				return "", errDown
			}
			return user(id), nil
		},
		ByLogin: func(context.Context, string) (user, error) { return "", auth.ErrNoUser },
	}, enc)
	if err != nil {
		t.Fatal(err)
	}
	acting := a.ActAs(context.WithValue(ctx, carriedKey{}, User("7")), "42")
	if got, err := ActorOf(acting); err != nil || got != User("42") {
		t.Errorf("acting user = %v, %v", got, err)
	}
	if _, err := ActorOf(a.ActAs(ctx, "down")); !errors.Is(err, errDown) {
		t.Errorf("a user who can't be loaded: %v", err)
	}
}

type user string

func (u user) AuthID() string       { return string(u) }
func (u user) AuthPassword() string { return "" }

func TestLimitAndKeys(t *testing.T) {
	if got := limit("ab€", 3); got != "ab" {
		t.Errorf("limit cut a character: %q", got)
	}
	if keyText([]byte{0xab}) != "ab" || keyText(int64(42)) != "42" {
		t.Error("keyText")
	}
}

func TestVia(t *testing.T) {
	tr := &Trail{cfg: Config{IP: "none"}}
	c, err := tr.contextOf(cmd.WithCommand(context.Background(), cmd.Command{Name: "db:prune-trashed"}))
	if err != nil || c.viaKind != "command" || c.viaName != "db:prune-trashed" || c.actor != System {
		t.Errorf("in a command: %+v, %v", c, err)
	}
	c, _ = tr.contextOf(context.WithValue(context.Background(), unitKey{}, anetos.Unit{Kind: "task", Name: "nightly"}))
	if c.viaKind != "task" || c.viaName != "nightly" {
		t.Errorf("in a unit: %+v", c)
	}
}

func TestSecretNames(t *testing.T) {
	for name, want := range map[string]bool{
		"password_hash": true, "api_token": true, "client_secret": true, "api_key": true, "apikey": true,
		"private_key": true, "session_key": true, "recovery_codes": true, "otp": true, "otp_secret": true, "credentials": true,
		"footprint": false, "title": false, "keyboard": false,
	} {
		if got := secretName.MatchString(name); got != want {
			t.Errorf("secretName(%q) = %v", name, got)
		}
	}
}

func TestConditionArgs(t *testing.T) {
	tr := &tracking{hidden: map[string]bool{"score": true}, masked: map[string]bool{"api_token": true}}
	args := []any{"x", 1}
	if got := tr.conditionArgs(`"t"."api_token" = $1 AND "t"."views" = $2`, args); got[0] != Redacted || got[1] != Redacted {
		t.Errorf("masked column: %v", got)
	}
	if got := tr.conditionArgs(`score > ?`, args); got[0] != Redacted {
		t.Errorf("hidden column: %v", got)
	}
	if got := tr.conditionArgs(`"t"."api_tokens_count" = $1`, args); got[0] != "x" {
		t.Errorf("a longer name was taken for the column: %v", got)
	}
}

func TestLengths(t *testing.T) {
	if checkLengths(strings.Repeat("k", 255), strings.Repeat("a", 100)) != nil {
		t.Error("lengths at the limits refused")
	}
	if checkLengths(strings.Repeat("k", 256), "") == nil || checkLengths("", strings.Repeat("a", 101)) == nil {
		t.Error("too long accepted")
	}
	if got := maskIP("fe80::1%eth0", "full"); got != "fe80::1" {
		t.Errorf("zone kept: %q", got)
	}
}

func TestUnknownCarriedActor(t *testing.T) {
	ctx := context.WithValue(context.Background(), carriedKey{}, errUnknownActor)
	if _, err := ActorOf(ctx); !errors.Is(err, errUnknownActor) {
		t.Errorf("ActorOf with an unknown dispatcher = %v", err)
	}
}
