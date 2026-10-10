---
title: Authorization
since: v0.2.0
group: "Accounts and security"
weight: 305
---

# Authorization

Decide what a logged-in user may do, with policies the compiler checks.

## Before you start

Set up [authentication](authentication.md): policies check the request's
logged-in user.

Policies decide from the user and the thing acted on ("authors edit
their own posts"). For roles stored per user, globally or in a team
("Ada is an owner of Acme"), use [roles and
permissions](roles-and-permissions.md); a policy can check those too,
with `rbac.CanIn`.

## Steps

### 1. Write policies

A policy is a function of the context, the user and (usually) the thing
acted on, returning whether the action is allowed. Group them as methods
of a type:

```go
// ManageUsers is for the admin page.
func (Policies) ManageUsers(_ context.Context, u *User) bool { return u.Admin }

// ViewUser lets users see their own profile, and admins everyone's.
func (Policies) ViewUser(_ context.Context, u *User, other *User) bool {
	return u.Admin || u.ID == other.ID
}
```

(Copied from [`examples/auth/users.go`](../../../examples/auth/users.go), region `policy`.)

### 2. Check them in handlers

```go
func (Accounts) ShowUser(c *web.Ctx, in UserID) (*User, error) {
	other, err := db.Find[User](c, in.ID)
	if err != nil {
		return nil, err
	}
	if err := auth.Authorize(c, policies.ViewUser, &other); err != nil {
		return nil, err // 403 unless it's you, or you're an admin
	}
	return &other, nil
}

func (Accounts) Admin(c *web.Ctx) error {
	if err := auth.AuthorizeUser(c, policies.ManageUsers); err != nil {
		return err
	}
	all, err := db.Query[User](c).OrderBy(colID.Asc()).Get()
	if err != nil {
		return err
	}
	return render(c, "admin", map[string]any{"Users": all})
}
```

(Copied from [`examples/auth`](../../../examples/auth/main.go), region `authorize`.)

`auth.Authorize` returns nil if the policy allows the action,
`auth.ErrUnauthenticated` (401) for a guest and `auth.ErrForbidden` (403)
otherwise, so a handler can return the error as it is.
`auth.AuthorizeUser` does the same for policies about the user alone.

### 3. Show or hide actions

`auth.Allows` and `auth.AllowsUser` report the answer as a boolean,
false for a guest:

```go
// illustrative
canEdit := auth.Allows(c, policies.UpdatePost, &post)
```

## How it works

Policies are plain Go functions: `Authorize[U, T]` infers the user type
`U` and the subject type `T` from the policy, finds the user with
`auth.Current[U]`, and calls it. A policy for posts can't be called with
a comment, and there are no ability names to misspell.

> **Coming from Laravel?** Policies and `$this->authorize('update', $post)`
> map to policy methods and `auth.Authorize(c, policies.Update, post)`;
> `@can` maps to `auth.Allows`. There are no string gates.

## Common problems

| Symptom | Cause | Fix |
|---|---|---|
| Every check returns 401 | The route lacks the auth middleware, or no one is logged in | Add `a.Middleware` (or `a.Require`) to the route |
| `the logged-in user isn't of the type asked for` | The policy's user type differs from the one given to `auth.New` | Use the same type (`*User` in both) |

## Next steps

- [Roles and permissions](roles-and-permissions.md)
- [Authentication](authentication.md)
