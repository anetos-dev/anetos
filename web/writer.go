// SPDX-License-Identifier: Apache-2.0

package web

import (
	"bufio"
	"io"
	"net"
	"net/http"
)

// responseWriter records the status code and bytes written so middleware
// (access logs) and error handling can tell whether a response has started.
// Unwrap lets http.ResponseController reach the underlying writer for
// Flush, Hijack, deadlines and so on.
type responseWriter struct {
	http.ResponseWriter
	status  int
	written int64
}

// wrapWriter returns w itself if it is already a *responseWriter.
func wrapWriter(w http.ResponseWriter) *responseWriter {
	if rw, ok := w.(*responseWriter); ok {
		return rw
	}
	return &responseWriter{ResponseWriter: w}
}

func (w *responseWriter) WriteHeader(code int) {
	// 1xx informational responses (such as 103 Early Hints) don't start
	// the real response.
	if w.status == 0 && (code >= 200 || code == http.StatusSwitchingProtocols) {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *responseWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.written += int64(n)
	return n, err
}

// Flush implements http.Flusher when the underlying writer supports it.
func (w *responseWriter) Flush() {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Hijack implements http.Hijacker for libraries that type-assert it (such
// as WebSocket upgraders). It returns http.ErrNotSupported if the
// underlying connection can't be hijacked.
func (w *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err == nil && w.status == 0 {
		w.status = http.StatusSwitchingProtocols
	}
	return conn, rw, err
}

// ReadFrom keeps io.Copy's sendfile fast path working through the wrapper.
func (w *responseWriter) ReadFrom(src io.Reader) (int64, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	var n int64
	var err error
	if rf, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		n, err = rf.ReadFrom(src)
	} else {
		n, err = io.Copy(struct{ io.Writer }{w.ResponseWriter}, src)
	}
	w.written += n
	return n, err
}

// started reports whether headers have been sent.
func (w *responseWriter) started() bool { return w.status != 0 }

// Status returns the response status, or 0 if nothing has been written yet.
func (w *responseWriter) Status() int { return w.status }
