# Teams: roles and permissions

A JSON API where users work in teams, made with package `auth/rbac`:

- **Permissions in code** ([`permissions.go`](permissions.go)): view,
  create and delete projects, manage members, delete teams, manage roles,
  view every team.
- **Roles in a team**: owner, member, guest, given in the team's scope
  (`team:42`). A team's members are the users with a role there.
- **Global roles**: admin (every permission, in every team) and support
  (views every team's projects).
- **Roles administrators add** (`POST /api/roles`), made of the declared
  permissions, stored in the database.
- **API tokens** narrow what their user may do: a token with only
  `projects.view` can't create projects, whatever its user's roles.
- An owner can make others owners, not administrators
  (`rbac.AuthorizeRole`).

## Run it

```sh
anetos key:generate           # APP_KEY, once (the anetos developer tool)
export APP_ENV=development HTTP_ADDR=:8080
go run . migrate
go run . seed                 # users, a team, and a token for each
go run .
```

Then, with a token that `seed` printed:

```sh
curl -H "Authorization: Bearer <token>" localhost:8080/api/teams
curl -H "Authorization: Bearer <token>" -d '{"name":"Rocket"}' localhost:8080/api/teams/1/projects
go run . rbac:user 3   # Bob's roles and permissions, by scope
go run . rbac:roles
```

The tests ([`main_test.go`](main_test.go)) show each rule. See the guide
[Roles and permissions](../../docs/site/guides/roles-and-permissions.md).
