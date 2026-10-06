// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"context"
	"fmt"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/cmd"
)

// addCommands adds audit:prune and audit:anonymize to the app.
func addCommands(app *anetos.App) error {
	for _, c := range []cmd.Command{
		{
			Name:        "audit:prune",
			Description: "Delete audit entries older than AUDIT_RETENTION_DAYS",
			Run: func(ctx context.Context, args *cmd.Args) error {
				if len(args.Args) > 0 {
					return cmd.Usagef("audit:prune takes no arguments")
				}
				p, err := Prune(ctx)
				if err != nil {
					return err
				}
				if p.Before.IsZero() {
					_, err = fmt.Fprintln(args.Stdout, "AUDIT_RETENTION_DAYS is 0: entries are kept forever.")
					return err
				}
				_, err = fmt.Fprintf(args.Stdout, "Deleted %d entries and %d bulk entries from before %s.\n", p.Entries, p.Bulk, p.Before.Format(time.DateOnly))
				return err
			},
		},
		{
			Name:        "audit:anonymize",
			Usage:       "<actor-type> <actor-id>",
			Description: "Replace an actor (user 42: user 42) with a placeholder in the audit log",
			Run: func(ctx context.Context, args *cmd.Args) error {
				if len(args.Args) != 2 {
					return cmd.Usagef("audit:anonymize takes an actor type and ID: user 42")
				}
				a := Actor{Type: args.Args[0], ID: args.Args[1]}
				if err := a.check(); err != nil {
					return cmd.Usagef("%v", err)
				}
				n, err := Anonymize(ctx, a)
				if err != nil {
					return err
				}
				_, err = fmt.Fprintf(args.Stdout, "Anonymized %s in %d entries.\n", a.Type, n)
				return err
			},
		},
	} {
		if err := app.AddCommand(c); err != nil {
			return err
		}
	}
	return nil
}
