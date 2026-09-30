// SPDX-License-Identifier: Apache-2.0

package dbtest

import (
	"context"
	"testing"

	"anetos.dev/anetos/auth/social"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
)

func init() {
	extra = append(extra, test{"SocialAccounts", testSocialAccounts})
}

// testSocialAccounts links provider accounts to users in the table
// social.Migrations creates.
func testSocialAccounts(t *testing.T, ctx context.Context) {
	r, err := migrate.NewRunner(d(ctx), []*migrate.Set{social.Migrations()}, migrate.WithTable("st_social_migrations"))
	check(t, err)
	_, err = r.Up(ctx)
	check(t, err)
	t.Cleanup(func() {
		ctx := context.WithoutCancel(ctx)
		_, _ = db.Exec(ctx, "DROP TABLE IF EXISTS social_accounts")
		_, _ = db.Exec(ctx, "DROP TABLE IF EXISTS st_social_migrations")
	})
	gh := social.Profile{Provider: "github", Subject: "7", Email: "octo@example.com"}
	if _, ok, err := social.FindLink(ctx, gh); ok || err != nil {
		t.Fatalf("before linking: %v %v", ok, err)
	}
	check(t, social.Link(ctx, gh, "1"))
	check(t, social.Link(ctx, social.Profile{Provider: "google", Subject: "g-1"}, "1"))
	if id, ok, err := social.FindLink(ctx, gh); !ok || err != nil || id != "1" {
		t.Errorf("FindLink = %q %v %v", id, ok, err)
	}
	// Another provider with the same subject is another account.
	if _, ok, _ := social.FindLink(ctx, social.Profile{Provider: "gitlab", Subject: "7"}); ok {
		t.Error("the subject matched another provider's account")
	}
	// Linking again moves the account.
	check(t, social.Link(ctx, gh, "2"))
	if id, _, _ := social.FindLink(ctx, gh); id != "2" {
		t.Errorf("after moving: %q", id)
	}
	links, err := social.Links(ctx, "1")
	check(t, err)
	if len(links) != 1 || links[0].Provider != "google" {
		t.Errorf("Links = %+v", links)
	}
	check(t, social.Unlink(ctx, "1", "google"))
	if links, _ := social.Links(ctx, "1"); len(links) != 0 {
		t.Errorf("after Unlink: %+v", links)
	}
}
