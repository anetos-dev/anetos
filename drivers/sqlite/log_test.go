// SPDX-License-Identifier: Apache-2.0

package sqlite_test

import (
	"io"
	"log/slog"
)

func slogTo(w io.Writer) *slog.Logger { return slog.New(slog.NewTextHandler(w, nil)) }
