// SPDX-License-Identifier: Apache-2.0

package password

import (
	"context"
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestHashAndVerify(t *testing.T) {
	h, err := Hash("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Errorf("hash %q", h)
	}
	if !Verify("correct horse", h) || Verify("correct horse ", h) || Verify("", h) {
		t.Error("Verify")
	}
	if h2, _ := Hash("correct horse"); h2 == h {
		t.Error("no salt")
	}
	if NeedsRehash(h) {
		t.Error("a default hash needs a rehash")
	}
	weak, _ := HashWith("pw", Params{Memory: 1024, Time: 1, Threads: 1})
	if !Verify("pw", weak) || !NeedsRehash(weak) {
		t.Error("weaker parameters")
	}
	strong, _ := HashWith("pw", Params{Memory: 32 * 1024, Time: 3, Threads: 2})
	if !Verify("pw", strong) || NeedsRehash(strong) {
		t.Error("a stronger hash needs a rehash")
	}
	if _, err := HashWith("pw", Params{Memory: 512 * 1024, Time: 1, Threads: 1}); err == nil {
		t.Error("huge memory accepted")
	}
	if _, err := Hash(strings.Repeat("x", MaxLength+1)); !errors.Is(err, ErrTooLong) {
		t.Errorf("too long: %v", err)
	}
	if Verify(strings.Repeat("x", MaxLength+1), h) {
		t.Error("an overlong password matched")
	}
	if _, err := HashWith("pw", Params{}); err == nil {
		t.Error("zero parameters accepted")
	}
}

func TestBcrypt(t *testing.T) {
	b, _ := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	laravel := "$2y$" + string(b[4:]) // Laravel's prefix
	for _, h := range []string{string(b), laravel} {
		if !Verify("secret", h) || Verify("Secret", h) {
			t.Errorf("bcrypt %q", h[:4])
		}
		if !NeedsRehash(h) || !IsBcrypt(h) {
			t.Error("bcrypt doesn't need a rehash")
		}
	}
}

func TestMalformed(t *testing.T) {
	for _, h := range []string{"", "plain", "$argon2id$", "$argon2i$v=19$m=1024,t=1,p=1$c2FsdA$a2V5",
		"$argon2id$v=19$m=4194304,t=1,p=1$c2FsdHNhbHRzYWx0$a2V5a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=19$m=1024,t=1,p=0$c2FsdHNhbHRzYWx0$a2V5a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=18$m=1024,t=1,p=1$c2FsdHNhbHRzYWx0$a2V5a2V5a2V5a2V5a2V5a2V5",
		"$argon2id$v=19$m=1024,t=1,p=1$!!$a2V5a2V5a2V5a2V5a2V5a2V5"} {
		if Verify("pw", h) {
			t.Errorf("%q matched", h)
		}
		if !NeedsRehash(h) {
			t.Errorf("%q doesn't need a rehash", h)
		}
	}
	Dummy("anything") // doesn't panic
}

func TestVerifyContext(t *testing.T) {
	h, _ := Hash("pw")
	ctx, cancel := context.WithCancel(context.Background())
	if ok, err := VerifyContext(ctx, "pw", h); !ok || err != nil {
		t.Fatalf("VerifyContext = %v, %v", ok, err)
	}
	// With every slot taken, a canceled context gives up.
	for range cap(slots) {
		slots <- struct{}{}
	}
	cancel()
	_, err := VerifyContext(ctx, "pw", h)
	for range cap(slots) {
		<-slots
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("waiting with a canceled context: %v", err)
	}
	if err := DummyContext(context.Background(), "x"); err != nil {
		t.Error(err)
	}
}
