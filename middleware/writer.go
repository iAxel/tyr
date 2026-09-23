package middleware

import (
	"bufio"
	"errors"
	"net"
	"net/http"
)

// responseWriter is the http.ResponseWriter that Logger and Recover pass
// on. It notes the status the handler sends and whether it hijacks the
// connection.
type responseWriter struct {
	http.ResponseWriter
	status   int  // the final status sent, 0 if none yet
	hijacked bool // whether the handler took over the connection
}

// wrap wraps w in a responseWriter. The writer it returns implements
// http.Hijacker if w does, for code that finds it by a type assertion, and
// doesn't otherwise, so that it claims no support w lacks: the HTTP/2
// writer has no Hijack.
func wrap(w http.ResponseWriter) (http.ResponseWriter, *responseWriter) {
	rw := &responseWriter{ResponseWriter: w}
	if _, ok := w.(http.Hijacker); ok {
		return hijacker{rw}, rw
	}
	return rw, rw
}

// started reports whether the response has started: its status went out,
// or the handler took over the connection.
func (w *responseWriter) started() bool {
	return w.status != 0 || w.hijacked
}

func (w *responseWriter) WriteHeader(code int) {
	w.ResponseWriter.WriteHeader(code)
	// More headers may follow a 1xx one, except 101 Switching Protocols,
	// which net/http treats as final.
	if w.status == 0 && (code >= 200 || code == http.StatusSwitchingProtocols) {
		w.status = code
	}
}

func (w *responseWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK // net/http sends it before the first write
	}
	return w.ResponseWriter.Write(b)
}

// Flush implements http.Flusher for code that finds it by a type
// assertion; it drops the error of FlushError.
func (w *responseWriter) Flush() {
	_ = w.FlushError()
}

// FlushError flushes the writer under w through http.ResponseController,
// which prefers it to Flush: it reports the error, such as
// http.ErrNotSupported.
func (w *responseWriter) FlushError() error {
	err := http.NewResponseController(w.ResponseWriter).Flush()
	if w.status == 0 && !errors.Is(err, http.ErrNotSupported) {
		w.status = http.StatusOK // flushing sends it first
	}
	return err
}

// Unwrap returns the writer under w, for http.ResponseController.
func (w *responseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// hijacker is a responseWriter over a writer that implements http.Hijacker.
type hijacker struct {
	*responseWriter
}

// Hijack implements http.Hijacker for code that finds it by a type
// assertion; it hijacks the writer under w through http.ResponseController.
func (w hijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, buf, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err == nil {
		w.hijacked = true
	}
	return conn, buf, err
}
