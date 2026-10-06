// SPDX-License-Identifier: Apache-2.0

package rbac

import (
	"context"
	"flag"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"text/tabwriter"

	"anetos.dev/anetos"
	"anetos.dev/anetos/cmd"
	"anetos.dev/anetos/db"
)

// addCommands adds the rbac:* commands to the app.
func addCommands(app *anetos.App) error {
	for _, c := range []cmd.Command{
		{
			Name:        "rbac:roles",
			Description: "List the roles, their permissions and how many users have them",
			Run: func(ctx context.Context, args *cmd.Args) error {
				if len(args.Args) > 0 {
					return cmd.Usagef("rbac:roles takes no arguments")
				}
				return listRoles(ctx, args.Stdout)
			},
		},
		{
			Name:        "rbac:user",
			Usage:       "<user-id>",
			Description: "Show a user's roles and permissions, by scope",
			Run: func(ctx context.Context, args *cmd.Args) error {
				if len(args.Args) != 1 {
					return cmd.Usagef("rbac:user takes a user ID")
				}
				return showUser(ctx, args.Stdout, args.Args[0])
			},
		},
		assignCommand("rbac:assign", "Give a user a role (globally, or in --scope=team:42)"),
		assignCommand("rbac:unassign", "Take a role away from a user (globally, or in --scope=team:42)"),
	} {
		if err := app.AddCommand(c); err != nil {
			return err
		}
	}
	return nil
}

func assignCommand(name, description string) cmd.Command {
	return cmd.Command{
		Name:        name,
		Usage:       "[--scope=team:42] <user-id> <role>",
		Description: description,
		Run: func(ctx context.Context, args *cmd.Args) error {
			fs := flag.NewFlagSet(name, flag.ContinueOnError)
			scope := fs.String("scope", "", "the scope (kind:id), empty for global")
			if err := args.Parse(fs); err != nil {
				return err
			}
			if fs.NArg() != 2 {
				return cmd.Usagef("%s takes a user ID and a role", name)
			}
			user, role, s := fs.Arg(0), fs.Arg(1), Scope(*scope)
			if name == "rbac:assign" {
				if err := Assign(ctx, user, s, role); err != nil {
					return err
				}
				_, err := fmt.Fprintf(args.Stdout, "User %s has role %s (%s).\n", user, role, s)
				return err
			}
			if err := Unassign(ctx, user, s, role); err != nil {
				return err
			}
			_, err := fmt.Fprintf(args.Stdout, "User %s no longer has role %s (%s).\n", user, role, s)
			return err
		},
	}
}

// listRoles prints the roles and warns of assignments of unknown roles.
func listRoles(ctx context.Context, w io.Writer) error {
	roles, err := Roles(ctx)
	if err != nil {
		return err
	}
	users, err := RoleCounts(ctx)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ROLE\tDEFINED IN\tUSERS\tPERMISSIONS")
	for _, r := range roles {
		where, perms := "code", "all (super)"
		if r.Custom {
			where = "database"
		}
		if !r.Super {
			names := make([]string, len(r.Permissions))
			for i, p := range r.Permissions {
				names[i] = string(p)
			}
			perms = strings.Join(names, ", ")
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\n", r.Name, where, users[r.Name], perms)
		delete(users, r.Name)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	for _, name := range slices.Sorted(maps.Keys(users)) {
		if _, err := fmt.Fprintf(w, "Warning: %d users have role %q, which is neither declared nor in the database: it allows nothing. Declare it again, or take it away (rbac:unassign).\n", users[name], name); err != nil {
			return err
		}
	}
	// Roles of the database that a role declared later in code replaces.
	stored, err := db.Pluck(db.Query[roleRow](ctx).OrderBy(colName.Asc()), colName)
	if err != nil {
		return err
	}
	reg, err := From(ctx)
	if err != nil {
		return err
	}
	for _, name := range stored {
		if reg.byName[name] != nil {
			if _, err := fmt.Fprintf(w, "Warning: role %q is declared in code and stored in the database: the code's applies to its users. Delete the stored one, or rename the code's.\n", name); err != nil {
				return err
			}
		}
	}
	return nil
}

// showUser prints a user's grants by scope.
func showUser(ctx context.Context, w io.Writer, userID string) error {
	g, err := Of(ctx, userID)
	if err != nil {
		return err
	}
	ss := make([]Scope, 0, len(g.g.byScope))
	for s := range g.g.byScope {
		ss = append(ss, s)
	}
	slices.Sort(ss)
	if len(ss) == 0 {
		_, err := fmt.Fprintf(w, "User %s has no roles or permissions.\n", userID)
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "SCOPE\tROLES\tPERMISSIONS (WITH GLOBAL ONES)")
	for _, s := range ss {
		perms := g.Permissions(s)
		names := make([]string, len(perms))
		for i, p := range perms {
			names[i] = string(p)
		}
		l := g.g.byScope[s]
		roles := slices.Clone(l.roles)
		for _, name := range l.stale {
			roles = append(roles, name+" (unknown: allows nothing)")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", s, strings.Join(roles, ", "), strings.Join(names, ", "))
	}
	return tw.Flush()
}
