// SPDX-License-Identifier: Apache-2.0

package web

import (
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

type order struct {
	ID int `json:"id"`
}

// A typed handler's result is written with the route's status; Empty
// results have no body.
func TestRouteStatus(t *testing.T) {
	r := newTestRouter()
	create := func(*Ctx, struct{}) (order, error) { return order{ID: 7}, nil }
	r.Post("/orders", H(create)).Status(http.StatusCreated)
	r.Get("/orders/7", H(create))
	r.Delete("/orders/7", H(func(*Ctx, struct{}) (Empty, error) { return Empty{}, nil }))
	r.Post("/orders/7/archive", H(func(*Ctx, struct{}) (Empty, error) { return Empty{}, nil })).Status(http.StatusAccepted)
	r.Post("/orders/7/touch", H(create)).Status(http.StatusNoContent)
	// A Responder chooses its own status.
	r.Post("/orders/7/copy", H(func(*Ctx, struct{}) (Responder, error) { return JSON(http.StatusOK, order{ID: 8}), nil })).Status(http.StatusCreated)
	// An error is written as usual.
	r.Post("/orders/9", H(func(*Ctx, struct{}) (order, error) { return order{}, Error(http.StatusNotFound, "no such order") })).Status(http.StatusCreated)

	for _, c := range []struct {
		method, path string
		status       int
		body         string
	}{
		{http.MethodPost, "/orders", 201, `{"id":7}`},
		{http.MethodGet, "/orders/7", 200, `{"id":7}`},
		{http.MethodDelete, "/orders/7", 204, ""},
		{http.MethodPost, "/orders/7/archive", 202, ""},
		{http.MethodPost, "/orders/7/touch", 204, ""},
		{http.MethodPost, "/orders/7/copy", 200, `{"id":8}`},
		{http.MethodPost, "/orders/9", 404, "no such order"},
	} {
		res := do(t, r, c.method, c.path, nil, "Accept", "application/json")
		if res.status != c.status || !strings.Contains(res.body, c.body) || (c.body == "" && res.body != "") {
			t.Errorf("%s %s: %d %q", c.method, c.path, res.status, res.body)
		}
	}

	for i, bad := range []int{0, 199, 301, 404, 500} {
		rt := r.Get("/bad/"+strconv.Itoa(i), H(create))
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("Status(%d) accepted", bad)
				}
			}()
			rt.Status(bad)
		}()
	}
}

// Outside a router (no route), Empty is still a 204.
func TestEmptyWithoutRoute(t *testing.T) {
	r := newTestRouter()
	h := H(func(*Ctx, struct{}) (Empty, error) { return Empty{}, nil })
	r.Get("/x", func(c *Ctx) error {
		c.route = nil
		return h(c)
	})
	if res := do(t, r, http.MethodGet, "/x", nil); res.status != http.StatusNoContent || res.body != "" {
		t.Errorf("%d %q", res.status, res.body)
	}
}

// Routes reports a typed handler's input and result types, and the
// route's status; two closures of one H[In, Out] keep their own types.
func TestTypedHandlerTypes(t *testing.T) {
	type in struct {
		ID int `path:"id"`
	}
	fn := func(*Ctx, in) (order, error) { return order{}, nil }
	a, b := H(fn), H(fn)
	if funcKey(a) == funcKey(b) {
		t.Fatal("two closures of one H share a key")
	}
	r := newTestRouter()
	r.Get("/orders/{id}", a)
	r.Post("/orders", H(func(*Ctx, struct{}) (order, error) { return order{}, nil })).Status(http.StatusCreated)
	r.Delete("/orders/{id}", H(func(*Ctx, in) (Empty, error) { return Empty{}, nil }))
	r.Get("/plain", func(*Ctx) error { return nil })

	want := []struct {
		in, out reflect.Type
		status  int
	}{
		{reflect.TypeFor[in](), reflect.TypeFor[order](), 0},
		{reflect.TypeFor[struct{}](), reflect.TypeFor[order](), http.StatusCreated},
		{reflect.TypeFor[in](), reflect.TypeFor[Empty](), 0},
		{nil, nil, 0},
	}
	routes := r.Routes()
	if len(routes) != len(want) {
		t.Fatalf("%d routes", len(routes))
	}
	for i, w := range want {
		got := routes[i]
		if got.Input != w.in || got.Output != w.out || got.Status != w.status {
			t.Errorf("%s %s: %v %v %d", got.Method, got.Pattern, got.Input, got.Output, got.Status)
		}
	}
}

type orders struct{}

func (orders) Show(*Ctx, struct{}) (order, error)   { return order{}, nil }
func (*orders) Store(*Ctx, struct{}) (order, error) { return order{}, nil }

// Routes names a typed handler's function, and reports what the route's
// documented middleware said, outermost first.
func TestRouteHandlerAndMiddleware(t *testing.T) {
	doc := func(scheme string, status int) Middleware {
		return func(next http.Handler) http.Handler {
			return Documented(next, MiddlewareDoc{Security: scheme, Responses: map[int]string{status: scheme}})
		}
	}
	plain := func(next http.Handler) http.Handler { return next }
	r := newTestRouter()
	g := r.Group("/api", doc("outer", 401), plain)
	g.With(doc("inner", 403)).Get("/orders", H(orders{}.Show))
	o := &orders{}
	g.Post("/orders", H(o.Store))
	g.Get("/fn", H(func(*Ctx, struct{}) (Empty, error) { return Empty{}, nil }))
	g.Get("/plain", func(*Ctx) error { return nil })

	routes := r.Routes()
	for i, want := range []struct {
		handler string
		docs    []string
	}{
		{"web.orders.Show", []string{"outer", "inner"}},
		{"web.orders.Store", []string{"outer"}},
		{"web.TestRouteHandlerAndMiddleware.func3", []string{"outer"}},
		{"", []string{"outer"}},
	} {
		var docs []string
		for _, d := range routes[i].Middleware {
			docs = append(docs, d.Security)
		}
		if routes[i].Handler != want.handler || !slices.Equal(docs, want.docs) {
			t.Errorf("%s: %q %v", routes[i].Pattern, routes[i].Handler, docs)
		}
	}
	// A documented handler serves as the handler it wraps.
	if res := do(t, r, http.MethodGet, "/api/orders", nil); res.status != http.StatusOK {
		t.Errorf("%d %s", res.status, res.body)
	}
}
