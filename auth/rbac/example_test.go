// SPDX-License-Identifier: Apache-2.0

package rbac_test

import (
	"fmt"

	"anetos.dev/anetos/auth/rbac"
)

func ExampleNew() {
	const (
		ViewProjects   rbac.Permission = "projects.view"
		CreateProjects rbac.Permission = "projects.create"
	)
	reg, err := rbac.New([]rbac.Permission{ViewProjects, CreateProjects},
		rbac.Role{Name: "owner", Permissions: []rbac.Permission{ViewProjects, CreateProjects}},
		rbac.Role{Name: "guest", Permissions: []rbac.Permission{ViewProjects}},
	)
	if err != nil {
		panic(err)
	}
	guest, _ := reg.Role("guest")
	fmt.Println(guest.Allows(ViewProjects), guest.Allows(CreateProjects))
	// Output: true false
}

func ExampleScopeOf() {
	team := rbac.ScopeOf("team", 42)
	fmt.Println(team, team.Kind(), team.ID())
	// Output: team:42 team 42
}
