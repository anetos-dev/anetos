// SPDX-License-Identifier: Apache-2.0

// Package ext is the public plugin API. A plugin is a Go package whose
// Plugin() constructor returns an [ext.Plugin]: a name, and any of the
// interfaces of what it adds (settings, migrations, routes, commands,
// jobs, scheduled tasks, event listeners, boot work). An app lists its
// plugins in plugins.go (written by `anetos add`) and wires them in at
// the end of setup with [Load]:
//
//	if err := ext.Load(app, plugins()); err != nil {
//		return nil, err
//	}
//
// Plugins use only the framework's public packages, as the app does;
// they are compiled into the app and run with its privileges.
package ext
