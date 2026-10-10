// SPDX-License-Identifier: Apache-2.0

// Package socialstub connects anetostest.FakeSocial to package
// auth/social: when the app provides a *Stub (anetos.Provide) before
// social.New runs, in APP_ENV=testing, every provider logs in
// through the stand-in OpenID Connect provider it describes. It is
// internal, so apps can't point their logins elsewhere with it.
package socialstub

import "net/http"

// Stub is a stand-in OpenID Connect provider.
type Stub struct {
	// Issuer is its https URL, the issuer of its ID tokens: its
	// authorization endpoint is Issuer/authorize, its token endpoint
	// Issuer/token.
	Issuer string
	// Client is the HTTP client that reaches it, trusting its
	// certificate.
	Client *http.Client
}
