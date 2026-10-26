// SPDX-License-Identifier: Apache-2.0

// Package typescript writes the frontend's TypeScript types from the
// app's Go types: the inputs and results of its typed handlers (web.H),
// with the JSON Schemas web/openapi's document has, and the props of its
// Inertia pages (web/inertia); with a client for the API's routes.
//
// [Register] adds the types command, which writes the files to commit
// beside the code (frontend/src/types/api.ts and pages.ts); go tool
// anetos generate and anetos dev run it, and [Check], in a test, fails
// when they are out of date:
//
//	// main.go's setup, after the routes
//	if err := typescript.Register(app, srv, typescript.Config{Prefix: "/api/v1", Inertia: in}); err != nil {
//		return nil, err
//	}
//
// The frontend imports them:
//
//	import type { PageProps } from '@/types/pages'
//	import { createApi } from '@/types/api'
//
// The files are built from types when asked for: nothing runs per
// request.
package typescript
