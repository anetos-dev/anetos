// SPDX-License-Identifier: Apache-2.0

// Package listeners holds the app's pub/sub listeners, which run with
// the app or alone (run --only=listeners).
package listeners

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"anetos.dev/anetos"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/pubsub"

	"anetos.dev/anetos/examples/saas/app/models"
)

// SubscriptionChanged is what the billing service publishes to the
// "billing.subscription_changed" topic when a customer subscribes,
// changes plan or cancels.
type SubscriptionChanged struct {
	Email string `json:"email"`
	Plan  string `json:"plan"` // free (canceled), pro or team
}

// ChangePlan sets the user's plan, which ends their trial. Applying a
// message twice changes nothing, so a redelivery is harmless; messages
// aren't ordered, though, so a late redelivery of an older change could
// undo a newer one: a real billing service would send a version to
// compare with the stored one.
func ChangePlan(ctx context.Context, m SubscriptionChanged) error {
	if !slices.Contains([]string{"free", "pro", "team"}, m.Plan) {
		return pubsub.Permanent(fmt.Errorf("unknown plan %q", m.Plan)) // straight to the dead-letter topic
	}
	n, err := db.Query[models.User](ctx).Where(models.UserCols.Email.Eq(strings.ToLower(m.Email))).
		Update(models.UserCols.Plan.Set(m.Plan), models.UserCols.TrialEndsAt.Set(nil))
	if err != nil {
		return err // retried
	}
	if n == 0 {
		return pubsub.Permanent(errors.New("no user with that email address"))
	}
	anetos.Logger(ctx).InfoContext(ctx, "plan changed", "plan", m.Plan)
	return nil
}
