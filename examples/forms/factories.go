// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"

	"anetos.dev/anetos/db/factory"
)

// region: factory
// NoteFactory makes valid notes, for the seeder and the tests. In a project
// made by anetos new, factories live in database/factories.
var NoteFactory = factory.New(func(n int) Note {
	return Note{Title: fmt.Sprintf("Note %d", n), Body: "Something to remember."}
})

// endregion
