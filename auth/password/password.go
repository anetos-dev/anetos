// SPDX-License-Identifier: Apache-2.0

// Package password hashes and checks passwords: argon2id for new hashes,
// with bcrypt hashes (from another framework, say Laravel) still
// accepted.
//
//	hash, err := password.Hash(in.Password)       // store hash
//	ok := password.Verify(in.Password, user.Password)
//	if ok && password.NeedsRehash(user.Password) { // after a login
//		// hash again and store it
//	}
//
// Hashes are PHC strings ("$argon2id$v=19$m=19456,t=2,p=1$salt$hash"), so
// the parameters travel with them and can be raised later.
package password

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

// Params are argon2id parameters.
type Params struct {
	// Memory is in KiB.
	Memory uint32
	// Time is the number of passes.
	Time uint32
	// Threads is the degree of parallelism.
	Threads uint8
}

// Defaults returns the parameters of new hashes: OWASP's recommended
// minimum for argon2id (19 MiB, 2 passes, 1 thread), about 20–40ms on a
// server core.
func Defaults() Params { return defaults }

var defaults = Params{Memory: 19 * 1024, Time: 2, Threads: 1}

// slots bounds the argon2 computations running at once, so a burst of
// logins can't take all the memory (each takes Params.Memory): the
// others wait.
var slots = make(chan struct{}, max(runtime.GOMAXPROCS(0), 2))

func idKey(ctx context.Context, pw, salt []byte, p Params, n uint32) ([]byte, error) {
	select {
	case slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-slots }()
	return argon2.IDKey(pw, salt, p.Time, p.Memory, p.Threads, n), nil
}

// MaxLength is the longest password accepted, in bytes: enough for any
// passphrase, and it keeps hashing cheap for attackers sending huge ones.
const MaxLength = 1024

const (
	saltLen   = 16
	keyLen    = 32
	maxMemory = 256 * 1024 // KiB
	maxTime   = 10
)

// ErrTooLong is returned by [Hash] for a password over [MaxLength] bytes.
var ErrTooLong = fmt.Errorf("password: longer than %d bytes", MaxLength)

// Hash returns the argon2id hash of pw with the [Defaults] parameters.
func Hash(pw string) (string, error) { return HashWith(pw, defaults) }

// HashWith returns the argon2id hash of pw with p.
func HashWith(pw string, p Params) (string, error) {
	if len(pw) > MaxLength {
		return "", ErrTooLong
	}
	if p.Memory < 8*uint32(p.Threads) || p.Time < 1 || p.Threads < 1 {
		return "", errors.New("password: invalid argon2id parameters")
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	if p.Memory > maxMemory || p.Time > maxTime {
		return "", errors.New("password: argon2id parameters out of range")
	}
	key, _ := idKey(context.Background(), []byte(pw), salt, p, keyLen)
	enc := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, p.Memory, p.Time, p.Threads,
		enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

// Verify reports whether pw matches hash, an argon2id or bcrypt hash. A
// malformed hash, or a password over [MaxLength] bytes, doesn't match.
// bcrypt only looks at a password's first 72 bytes, so for a bcrypt hash
// a longer password matches if its start does.
func Verify(pw, hash string) bool {
	ok, _ := VerifyContext(context.Background(), pw, hash)
	return ok
}

// VerifyContext is [Verify] that stops waiting for a free slot (only a
// few hashes are computed at once) when ctx ends, with ctx's error.
func VerifyContext(ctx context.Context, pw, hash string) (bool, error) {
	if len(pw) > MaxLength {
		return false, nil
	}
	if isBcrypt(hash) {
		return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil, nil
	}
	p, salt, key, err := parse(hash)
	if err != nil {
		return false, nil //nolint:nilerr // a malformed hash doesn't match
	}
	got, err := idKey(ctx, []byte(pw), salt, p, uint32(len(key)))
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(got, key) == 1, nil
}

// NeedsRehash reports whether hash should be replaced by a new [Hash]
// the next time the password is known (at login): it is bcrypt, or
// argon2id weaker (less memory or fewer passes) than [Defaults]. Stronger
// hashes are kept.
func NeedsRehash(hash string) bool {
	p, _, _, err := parse(hash)
	return err != nil || p.Memory < defaults.Memory || p.Time < defaults.Time
}

// IsBcrypt reports whether hash is a bcrypt hash, which ignores password
// bytes after the 72nd.
func IsBcrypt(hash string) bool { return isBcrypt(hash) }

// Dummy verifies pw against a fixed hash and ignores the result. Call it
// when the user isn't found, so that a login takes as long either way and
// doesn't reveal which accounts exist.
func Dummy(pw string) { Verify(pw, dummyHash()) }

// DummyContext is [Dummy] that stops waiting for a free slot when ctx
// ends.
func DummyContext(ctx context.Context, pw string) error {
	_, err := VerifyContext(ctx, pw, dummyHash())
	return err
}

var dummyHash = sync.OnceValue(func() string {
	h, err := Hash("anetos dummy password")
	if err != nil {
		panic(err) // can't happen: a short password, the default parameters
	}
	return h
})

func isBcrypt(hash string) bool {
	return strings.HasPrefix(hash, "$2a$") || strings.HasPrefix(hash, "$2b$") || strings.HasPrefix(hash, "$2y$")
}

// parse splits an argon2id PHC string.
func parse(hash string) (p Params, salt, key []byte, err error) {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return p, nil, nil, errors.New("password: not an argon2id hash")
	}
	var v int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &v); err != nil || v != argon2.Version {
		return p, nil, nil, errors.New("password: unsupported argon2 version")
	}
	if n, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Threads); err != nil || n != 3 {
		return p, nil, nil, errors.New("password: bad argon2id parameters")
	}
	// Refuse parameters that would make one check take seconds or
	// hundreds of megabytes (a hash from somewhere else, or tampered with).
	if p.Memory > maxMemory || p.Time > maxTime || p.Threads < 1 || p.Memory < 8*uint32(p.Threads) || p.Time < 1 {
		return p, nil, nil, errors.New("password: argon2id parameters out of range")
	}
	enc := base64.RawStdEncoding
	if salt, err = enc.DecodeString(parts[4]); err != nil {
		return p, nil, nil, err
	}
	if key, err = enc.DecodeString(parts[5]); err != nil || len(key) < 16 || len(key) > 64 {
		return p, nil, nil, errors.New("password: bad argon2id hash")
	}
	return p, salt, key, nil
}
