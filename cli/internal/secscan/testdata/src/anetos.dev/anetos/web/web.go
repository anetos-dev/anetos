// SPDX-License-Identifier: Apache-2.0

// Package web is a stub of Anetos's, for the analyzer's tests.
package web

import "net/http"

type Ctx struct{ r *http.Request }

func (c *Ctx) Param(name string) string               { return "" }
func (c *Ctx) Query(name string) string               { return "" }
func (c *Ctx) Header(name string) string              { return "" }
func (c *Ctx) Request() *http.Request                 { return c.r }
func (c *Ctx) Redirect(status int, url string) error  { return nil }
func (c *Ctx) HTML(status int, html string) error     { return nil }
func (c *Ctx) Text(status int, s string) error        { return nil }
func (c *Ctx) Deadline() (deadline struct{}, ok bool) { return }

type HandlerFunc func(c *Ctx) error

type Responder interface{ Respond(c *Ctx) error }

func Redirect(url string) Responder { return nil }

func H[In, Out any](fn func(c *Ctx, in In) (Out, error)) HandlerFunc { return nil }
