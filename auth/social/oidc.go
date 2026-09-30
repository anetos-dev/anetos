// SPDX-License-Identifier: Apache-2.0

package social

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// discoveryBackoff is how long a failed discovery is remembered, so that
// an outage at the provider doesn't turn every sign-in into a request;
// discoveryTTL is how long a discovered document is used before it is
// read again (in the background, keeping the old one if that fails).
const (
	discoveryBackoff = 30 * time.Second
	discoveryTTL     = 24 * time.Hour
)

// discover returns the endpoints of an OpenID Connect provider, from its
// discovery document: fetched in the background by one request while
// others wait (or give up with their context); a failure is retried after
// discoveryBackoff, and a success refreshed after discoveryTTL.
func (s *Social[U]) discover(ctx context.Context, p *provider) (oauth2.Endpoint, error) {
	for {
		p.mu.Lock()
		switch {
		case p.found:
			if s.now().Sub(p.foundAt) > discoveryTTL && p.loading == nil {
				s.fetchInBackground(p)
			}
			defer p.mu.Unlock()
			return p.endpoint, nil
		case p.failed != nil && s.now().Sub(p.failedAt) < discoveryBackoff:
			defer p.mu.Unlock()
			return oauth2.Endpoint{}, p.failed
		case p.loading == nil:
			s.fetchInBackground(p)
		}
		wait := p.loading
		p.mu.Unlock()
		select {
		case <-wait:
			p.mu.Lock()
			if !p.found && p.failed != nil {
				err := p.failed
				p.mu.Unlock()
				return oauth2.Endpoint{}, err
			}
			p.mu.Unlock()
		case <-ctx.Done():
			return oauth2.Endpoint{}, ctx.Err()
		}
	}
}

// fetchInBackground starts reading the discovery document, so that the
// request that started it can give up like the others; call with p.mu
// held. A refresh that fails keeps the endpoints found before.
func (s *Social[U]) fetchInBackground(p *provider) {
	done := make(chan struct{})
	p.loading = done
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		e, err := s.fetchDiscovery(ctx, p)
		cancel()
		p.mu.Lock()
		p.loading = nil
		switch {
		case err == nil:
			p.endpoint, p.found, p.foundAt, p.failed = e, true, s.now(), nil
		case p.found:
			p.foundAt = s.now() // keep the old endpoints; try again after the TTL
			s.log.Warn("social: refreshing the discovery document failed; keeping the old one", "provider", p.Name, "error", err)
		default:
			p.failed, p.failedAt = err, s.now()
		}
		p.mu.Unlock()
		close(done)
	}()
}

// fetchDiscovery reads the provider's discovery document.
func (s *Social[U]) fetchDiscovery(ctx context.Context, p *provider) (oauth2.Endpoint, error) {
	u := strings.TrimSuffix(p.Issuer, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return oauth2.Endpoint{}, err
	}
	res, err := s.client.Do(req)
	if err != nil {
		return oauth2.Endpoint{}, fmt.Errorf("social: discover %s: %w", p.Name, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return oauth2.Endpoint{}, fmt.Errorf("social: discover %s: %s", p.Name, res.Status)
	}
	var doc struct {
		Issuer        string `json:"issuer"`
		Authorization string `json:"authorization_endpoint"`
		Token         string `json:"token_endpoint"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&doc); err != nil {
		return oauth2.Endpoint{}, fmt.Errorf("social: discover %s: %w", p.Name, err)
	}
	if doc.Issuer != p.Issuer { // exactly (OpenID Connect Discovery 4.3)
		return oauth2.Endpoint{}, fmt.Errorf("social: %s's discovery document is for issuer %q, not %q", p.Name, doc.Issuer, p.Issuer)
	}
	for _, e := range []string{doc.Authorization, doc.Token} {
		if pu, err := url.Parse(e); err != nil || pu.Scheme != "https" || pu.Host == "" {
			return oauth2.Endpoint{}, fmt.Errorf("social: %s's endpoint %q isn't an https URL", p.Name, e)
		}
	}
	return oauth2.Endpoint{AuthURL: doc.Authorization, TokenURL: doc.Token}, nil
}

// claims are the ID token claims that are checked and used.
type claims struct {
	Issuer        string          `json:"iss"`
	Subject       string          `json:"sub"`
	Audience      audience        `json:"aud"`
	AuthorizedFor string          `json:"azp"`
	Expires       int64           `json:"exp"`
	IssuedAt      int64           `json:"iat"`
	Nonce         string          `json:"nonce"`
	Email         string          `json:"email"`
	EmailVerified json.RawMessage `json:"email_verified"`
	Name          string          `json:"name"`
	Picture       string          `json:"picture"`
}

// audience is "aud": a string or a list of them.
type audience []string

func (a *audience) UnmarshalJSON(b []byte) error {
	var one string
	if json.Unmarshal(b, &one) == nil {
		*a = audience{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*a = many
	return nil
}

// leeway allows for clocks that differ between the app and the provider.
const leeway = time.Minute

// idToken checks an ID token received from the token endpoint and returns
// its profile. Its signature isn't checked: it came from the token
// endpoint over TLS, which OpenID Connect Core (3.1.3.7) allows to stand
// in for it. Everything else is: issuer, audience, expiry and nonce.
func (s *Social[U]) idToken(p *provider, raw, nonce string) (Profile, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return Profile{}, fmt.Errorf("social: %s gave no ID token", p.Name)
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return Profile{}, fmt.Errorf("social: %s's ID token: %w", p.Name, err)
	}
	var c claims
	if err := json.Unmarshal(payload, &c); err != nil {
		return Profile{}, fmt.Errorf("social: %s's ID token: %w", p.Name, err)
	}
	now := s.now()
	switch {
	case c.Issuer != p.Issuer && !slices.Contains(p.Issuers, c.Issuer):
		return Profile{}, fmt.Errorf("social: %s's ID token is from issuer %q", p.Name, c.Issuer)
	case !slices.Contains(c.Audience, p.clientID):
		return Profile{}, fmt.Errorf("social: %s's ID token isn't for this app (aud %v)", p.Name, c.Audience)
	case (len(c.Audience) > 1 || c.AuthorizedFor != "") && c.AuthorizedFor != p.clientID:
		return Profile{}, fmt.Errorf("social: %s's ID token is authorized for %q", p.Name, c.AuthorizedFor)
	case c.Expires == 0:
		return Profile{}, fmt.Errorf("social: %s's ID token has no expiry", p.Name)
	case now.After(time.Unix(c.Expires, 0).Add(leeway)):
		return Profile{}, fmt.Errorf("social: %s's ID token expired", p.Name)
	case c.IssuedAt != 0 && time.Unix(c.IssuedAt, 0).After(now.Add(leeway)):
		return Profile{}, fmt.Errorf("social: %s's ID token is from the future", p.Name)
	case nonce == "" || c.Nonce != nonce:
		return Profile{}, errors.New("social: the ID token's nonce doesn't match the session's")
	}
	return Profile{
		Subject:       c.Subject,
		Email:         c.Email,
		EmailVerified: verified(c.EmailVerified),
		Name:          c.Name,
		AvatarURL:     c.Picture,
	}, nil
}

// verified reads email_verified, a boolean that some providers send as a
// string.
func verified(raw json.RawMessage) bool {
	var b bool
	if json.Unmarshal(raw, &b) == nil {
		return b
	}
	var s string
	return json.Unmarshal(raw, &s) == nil && s == "true"
}
