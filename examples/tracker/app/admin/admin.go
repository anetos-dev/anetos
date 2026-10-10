// SPDX-License-Identifier: Apache-2.0

// Package admin has the admin interface's resources (anetos make:admin):
// what it lists and edits of each model. `anetos make:admin-resource
// <Model>` writes one and adds it to Resources.
package admin

import "anetos.dev/anetos/admin"

// Resources add the admin's resources, in the order of its menu.
var Resources = []func(p *admin.Panel) error{
	Projects,
	Issues,
}
