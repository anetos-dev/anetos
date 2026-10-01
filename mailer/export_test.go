// SPDX-License-Identifier: Apache-2.0

package mailer

import (
	"crypto/x509"
	"time"
)

// SetRootCAs makes t trust the certificates of pool.
func SetRootCAs(t *SMTPTransport, pool *x509.CertPool) { t.rootCAs = pool }

// RequireSTARTTLS makes t require STARTTLS, as for a remote host.
func RequireSTARTTLS(t *SMTPTransport) { t.starttls = "required" }

// SetNow replaces m's clock.
func SetNow(m *Mailer, now func() time.Time) { m.now = now }
