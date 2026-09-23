package middleware_test

import (
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	"github.com/iaxel/tyr/middleware"
)

func TestLogger(t *testing.T) {
	tests := []struct {
		name    string
		path    string            // "/links/go" if empty
		handler http.HandlerFunc  // at GET /links/{code}
		want    map[string]string // the attributes other than the method, route and duration
	}{
		{
			name:    "status",
			handler: func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusCreated) },
			want:    map[string]string{"status": "201"},
		},
		{
			name:    "no status",
			handler: func(w http.ResponseWriter, r *http.Request) {},
			want:    map[string]string{"status": "200"},
		},
		{
			name:    "write",
			handler: func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "https://go.dev") },
			want:    map[string]string{"status": "200"},
		},
		{
			name: "1xx first",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusEarlyHints)
				w.WriteHeader(http.StatusNoContent)
			},
			want: map[string]string{"status": "204"},
		},
		{
			name: "second status",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusCreated)
				w.WriteHeader(http.StatusInternalServerError) // net/http drops it
			},
			want: map[string]string{"status": "201"},
		},
		{
			name:    "flush",
			handler: func(w http.ResponseWriter, r *http.Request) { w.(http.Flusher).Flush() },
			want:    map[string]string{"status": "200"},
		},
		{
			name: "no route",
			path: "/nowhere",
			want: map[string]string{"route": "", "status": "404"},
		},
		{
			name: "aborted",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, "partial")
				w.(http.Flusher).Flush()
				panic(http.ErrAbortHandler)
			},
			want: map[string]string{"status": "200", "aborted": "true"},
		},
		{
			name:    "aborted before the status",
			handler: func(w http.ResponseWriter, r *http.Request) { panic(http.ErrAbortHandler) },
			want:    map[string]string{"status": "0", "aborted": "true"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				mux := http.NewServeMux()
				if tt.handler != nil {
					mux.Handle("GET /links/{code}", tt.handler)
				}
				slow := func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						time.Sleep(time.Second)
						next.ServeHTTP(w, r)
					})
				}
				logs := &logs{}
				client, _ := serve(t, middleware.Chain(mux, middleware.Logger(slog.New(logs)), slow))

				path := "/links/go"
				if tt.path != "" {
					path = tt.path
				}
				if resp, err := client.Get("http://example.com" + path); err == nil {
					_, _ = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
				}
				synctest.Wait()

				want := map[string]string{"method": "GET", "route": "GET /links/{code}", "duration": "1s"}
				maps.Copy(want, tt.want)
				got := logs.get()
				if len(got) != 1 || got[0].level != slog.LevelInfo || got[0].msg != "middleware: request" || !maps.Equal(got[0].attrs, want) {
					t.Errorf("logged %+v, want one middleware: request at INFO with %v", got, want)
				}
			})
		})
	}
}

// Not parallel: it replaces the default logger.
func TestNilLogger(t *testing.T) {
	h := middleware.Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}), middleware.Logger(nil), middleware.Recover(nil))

	// Both log to the default logger as it is at the time of writing.
	logs := &logs{}
	defer slog.SetDefault(slog.Default())
	slog.SetDefault(slog.New(logs))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))

	got := logs.get()
	if len(got) != 2 || got[0].msg != "middleware: panic" || got[1].msg != "middleware: request" {
		t.Errorf("the default logger got %+v, want middleware: panic and middleware: request", got)
	}
}
