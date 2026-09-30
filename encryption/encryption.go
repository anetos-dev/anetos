// SPDX-License-Identifier: Apache-2.0

// Package encryption encrypts and authenticates small messages, such as
// session cookies, with the application key (APP_KEY).
//
//	enc, err := encryption.ForApp(app)
//	token := enc.EncryptString("user:42", "password-reset")
//	msg, err := enc.DecryptString(token, "password-reset")
//
// Messages are sealed with AES-256-GCM under a key derived for each
// message (HKDF-SHA256 with a random salt), so there is no practical limit
// on how many messages one key can encrypt. The context string is
// authenticated with the message: a ciphertext made for one purpose can't
// be used for another. Keys rotate by moving the old key to
// APP_PREVIOUS_KEYS: new messages use APP_KEY, old ones still decrypt.
//
// An Encrypter is safe for concurrent use.
package encryption

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"

	"anetos.dev/anetos"
	"anetos.dev/anetos/internal/appkey"
)

// KeySize is the size of a key in bytes.
const KeySize = appkey.Size

// ErrInvalid is returned for a message that was tampered with, made for
// another context, or encrypted with a key that is no longer configured.
var ErrInvalid = errors.New("encryption: invalid message")

const (
	version  = 1
	idSize   = 4
	saltSize = 16
	overhead = 1 + idSize + saltSize + 16 // version, key id, salt, GCM tag
	hkdfInfo = "anetos encryption v1"
)

// Encrypter encrypts with its first key and decrypts with any of them.
type Encrypter struct {
	keys []key
}

type key struct {
	id     [idSize]byte
	secret []byte
}

// New returns an Encrypter that encrypts with current and also decrypts
// messages written with the previous keys. Every key must be [KeySize]
// bytes.
func New(current []byte, previous ...[]byte) (*Encrypter, error) {
	e := &Encrypter{}
	for i, k := range append([][]byte{current}, previous...) {
		if len(k) != KeySize {
			return nil, fmt.Errorf("encryption: key %d is %d bytes, want %d", i, len(k), KeySize)
		}
		sum := sha256.Sum256(append([]byte("anetos key id\x00"), k...))
		kk := key{secret: append([]byte(nil), k...)}
		copy(kk.id[:], sum[:idSize])
		e.keys = append(e.keys, kk)
	}
	return e, nil
}

// ForApp returns an Encrypter for the application's APP_KEY and
// APP_PREVIOUS_KEYS. It fails, suggesting a freshly generated key, if
// APP_KEY is not set.
func ForApp(app *anetos.App) (*Encrypter, error) {
	cfg := app.Config()
	if cfg.Key == "" {
		return nil, fmt.Errorf("encryption: APP_KEY is not set; add a key to your environment or .env, for example:\n\n\tAPP_KEY=%s", GenerateKey())
	}
	current, err := ParseKey(string(cfg.Key))
	if err != nil {
		return nil, fmt.Errorf("APP_KEY: %w", err)
	}
	var previous [][]byte
	for i, s := range cfg.PreviousKeys {
		k, err := ParseKey(string(s))
		if err != nil {
			return nil, fmt.Errorf("APP_PREVIOUS_KEYS[%d]: %w", i, err)
		}
		previous = append(previous, k)
	}
	return New(current, previous...)
}

// ParseKey decodes a key written as "base64:…", the form of APP_KEY.
func ParseKey(s string) ([]byte, error) {
	k, err := appkey.Parse(s)
	if err != nil {
		return nil, fmt.Errorf("encryption: %w", err)
	}
	return k, nil
}

// GenerateKey returns a new random key in the "base64:…" form of APP_KEY.
func GenerateKey() string { return appkey.Generate() }

// Encrypt seals plaintext for the given context (any string naming the
// purpose, such as a cookie name) and returns the ciphertext.
func (e *Encrypter) Encrypt(plaintext []byte, context string) []byte {
	k := e.keys[0]
	out := make([]byte, 1+idSize+saltSize, overhead+len(plaintext))
	out[0] = version
	copy(out[1:], k.id[:])
	salt := out[1+idSize:]
	_, _ = rand.Read(salt) // never fails
	return k.aead(salt).Seal(out, zeroNonce[:], plaintext, []byte(context))
}

// Decrypt opens a ciphertext made by Encrypt with the same context. It
// returns [ErrInvalid] if the message was changed, made for another
// context, or sealed with a key that isn't configured.
func (e *Encrypter) Decrypt(ciphertext []byte, context string) ([]byte, error) {
	if len(ciphertext) < overhead || ciphertext[0] != version {
		return nil, ErrInvalid
	}
	id := ciphertext[1 : 1+idSize]
	salt := ciphertext[1+idSize : 1+idSize+saltSize]
	for _, k := range e.keys {
		if string(k.id[:]) != string(id) {
			continue
		}
		plain, err := k.aead(salt).Open(nil, zeroNonce[:], ciphertext[1+idSize+saltSize:], []byte(context))
		if err != nil {
			return nil, ErrInvalid
		}
		return plain, nil
	}
	return nil, ErrInvalid
}

// EncryptString is Encrypt for strings; the result is URL-safe base64
// without padding, fit for cookies and URLs.
func (e *Encrypter) EncryptString(plaintext, context string) string {
	return base64.RawURLEncoding.EncodeToString(e.Encrypt([]byte(plaintext), context))
}

// DecryptString reverses EncryptString.
func (e *Encrypter) DecryptString(ciphertext, context string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", ErrInvalid
	}
	plain, err := e.Decrypt(b, context)
	return string(plain), err
}

// The nonce can be fixed because every message has its own key.
var zeroNonce [12]byte

// aead derives the message key from the salt.
func (k key) aead(salt []byte) cipher.AEAD {
	mk, err := hkdf.Key(sha256.New, k.secret, salt, hkdfInfo, KeySize)
	if err != nil {
		panic(err) // only for invalid lengths, which can't happen
	}
	block, err := aes.NewCipher(mk)
	if err != nil {
		panic(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	return gcm
}
