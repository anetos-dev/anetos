// SPDX-License-Identifier: Apache-2.0

package bench

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"testing"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"
	"github.com/labstack/echo/v4"
)

// The same hello and JSON work in other Go routers and frameworks, bare
// (no middleware), for comparison with BenchmarkHelloRouter and
// BenchmarkJSONRouter: chi (net/http handlers, decoding by hand, as
// BenchmarkJSONMux), Gin and Echo (their binding, with
// go-playground/validator for the same two rules). Fiber is left out: it
// runs on fasthttp, not net/http, so it can't serve the same
// in-process requests.

func BenchmarkHelloChi(b *testing.B) {
	r := chi.NewRouter()
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "Hello, world!")
	})
	serve(b, r, get("/"), http.StatusOK)
}

func BenchmarkJSONChi(b *testing.B) {
	r := chi.NewRouter()
	r.Post("/authors/{id}/posts", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		var in createPost
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		if in.Title == "" || utf8.RuneCountInString(in.Title) > 200 || in.Body == "" {
			http.Error(w, "invalid", http.StatusUnprocessableEntity)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(post{ID: 1, AuthorID: id, Title: in.Title, Body: in.Body})
	})
	serve(b, r, postReq, http.StatusCreated)
}

// createPostTags is createPost with the tags Gin and Echo bind and
// validate with.
type createPostTags struct {
	AuthorID int64  `param:"id"` // Echo; Gin reads c.Param
	Title    string `json:"title" binding:"required,max=200" validate:"required,max=200"`
	Body     string `json:"body" binding:"required" validate:"required"`
}

func BenchmarkHelloGin(b *testing.B) {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "Hello, world!") })
	serve(b, r, get("/"), http.StatusOK)
}

func BenchmarkJSONGin(b *testing.B) {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.POST("/authors/:id/posts", func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64) // ShouldBindUri would validate the body's fields too
		if err != nil {
			c.String(http.StatusBadRequest, "bad id")
			return
		}
		var in createPostTags
		if err := c.ShouldBindJSON(&in); err != nil {
			c.String(http.StatusUnprocessableEntity, "invalid")
			return
		}
		c.JSON(http.StatusCreated, post{ID: 1, AuthorID: id, Title: in.Title, Body: in.Body})
	})
	serve(b, r, postReq, http.StatusCreated)
}

type echoValidator struct{ v *validator.Validate }

func (e echoValidator) Validate(i any) error { return e.v.Struct(i) }

func BenchmarkHelloEcho(b *testing.B) {
	e := echo.New()
	e.GET("/", func(c echo.Context) error { return c.String(http.StatusOK, "Hello, world!") })
	serve(b, e, get("/"), http.StatusOK)
}

func BenchmarkJSONEcho(b *testing.B) {
	e := echo.New()
	e.Validator = echoValidator{validator.New()}
	e.POST("/authors/:id/posts", func(c echo.Context) error {
		var in createPostTags
		if err := c.Bind(&in); err != nil {
			return c.String(http.StatusBadRequest, "bad request")
		}
		if err := c.Validate(&in); err != nil {
			return c.String(http.StatusUnprocessableEntity, "invalid")
		}
		return c.JSON(http.StatusCreated, post{ID: 1, AuthorID: in.AuthorID, Title: in.Title, Body: in.Body})
	})
	serve(b, e, postReq, http.StatusCreated)
}
