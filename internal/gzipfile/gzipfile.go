// SPDX-License-Identifier: Apache-2.0

// Package gzipfile serves static files gzipped: the parts package view's
// Assets and package vite share.
package gzipfile

import (
	"bytes"
	"compress/gzip"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
)

// Compress returns b gzipped, or nil when that doesn't shrink it by a
// tenth.
func Compress(b []byte) []byte {
	var out bytes.Buffer
	w, _ := gzip.NewWriterLevel(&out, gzip.BestCompression) // a valid level: no error
	if _, err := w.Write(b); err != nil {
		return nil
	}
	if err := w.Close(); err != nil || out.Len() > len(b)*9/10 {
		return nil
	}
	return bytes.Clone(out.Bytes())
}

// Compressible reports whether a file's type is text, which gzip shrinks.
func Compressible(name string) bool {
	t, _, _ := strings.Cut(mime.TypeByExtension(path.Ext(name)), ";")
	switch t {
	case "application/javascript", "text/javascript", "application/json", "application/manifest+json",
		"image/svg+xml", "application/xml", "application/wasm":
		return true
	}
	return strings.HasPrefix(t, "text/")
}

// AcceptsGzip reports whether a request's Accept-Encoding takes gzip:
// it names gzip with a weight above 0 (a "*" alone doesn't count).
func AcceptsGzip(r *http.Request) bool {
	for _, v := range r.Header.Values("Accept-Encoding") {
		for part := range strings.SplitSeq(v, ",") {
			coding, params, _ := strings.Cut(part, ";")
			if !strings.EqualFold(strings.TrimSpace(coding), "gzip") {
				continue
			}
			for param := range strings.SplitSeq(params, ";") {
				name, value, _ := strings.Cut(param, "=")
				if strings.EqualFold(strings.TrimSpace(name), "q") {
					q, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
					return err == nil && q > 0
				}
			}
			return true
		}
	}
	return false
}

// Writer marks a response gzipped when it has content (200, 206):
// http.ServeContent sets the length of the gzipped bytes only while the
// header has no Content-Encoding, and a 304 or an error isn't gzipped.
type Writer struct{ http.ResponseWriter }

// WriteHeader adds Content-Encoding to a response with content.
func (w Writer) WriteHeader(code int) {
	if code == http.StatusOK || code == http.StatusPartialContent {
		w.Header().Set("Content-Encoding", "gzip")
	}
	w.ResponseWriter.WriteHeader(code)
}

// Unwrap returns the response writer, for http.ResponseController.
func (w Writer) Unwrap() http.ResponseWriter { return w.ResponseWriter }
