// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"anetos.dev/anetos/anetostest"
)

// anetostest.New boots the app with test settings (.env.testing, not
// .env) and a migrated database, which the test leaves as it found it.
func TestHome(t *testing.T) {
	app := anetostest.New(t, setup)
	app.Get("/").AssertOK().AssertSee("SaaS")
}
