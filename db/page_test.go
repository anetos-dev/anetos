// SPDX-License-Identifier: Apache-2.0

package db_test

import (
	"strconv"
	"testing"

	"anetos.dev/anetos/db"
)

func TestMapPage(t *testing.T) {
	p := db.Page[int]{Data: []int{1, 2}, CurrentPage: 2, PerPage: 2, Total: 5, LastPage: 3}
	got := db.MapPage(p, strconv.Itoa)
	if len(got.Data) != 2 || got.Data[0] != "1" || got.Data[1] != "2" || got.CurrentPage != 2 || got.PerPage != 2 ||
		got.Total != 5 || got.LastPage != 3 {
		t.Errorf("%+v", got)
	}
	// An empty page stays a list ([] in JSON), not nil.
	if empty := db.MapPage(db.Page[int]{Data: []int{}, LastPage: 1}, strconv.Itoa); empty.Data == nil {
		t.Error("nil data")
	}
}
