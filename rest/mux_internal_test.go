package rest

import (
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

// fakeMux replies to every request itself, with an empty pattern, as a
// ServeMux of another version of Go might.
type fakeMux struct {
	reply http.HandlerFunc
}

func (m fakeMux) Handler(*http.Request) (http.Handler, string) {
	return m.reply, ""
}

func (m fakeMux) ServeHTTP(http.ResponseWriter, *http.Request) {
	panic("fakeMux serves no routes")
}

func TestProblemHandlerOtherReplies(t *testing.T) {
	tests := []struct {
		name  string
		reply http.HandlerFunc
	}{
		{"redirect", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/clean", http.StatusMovedPermanently)
		}},
		{"bad request", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "bad host", http.StatusBadRequest)
		}},
		{"no status", func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "hi")
		}},
		{"404 after a body", func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "partial")
			w.WriteHeader(http.StatusNotFound)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The reply goes out as the mux sends it.
			want := httptest.NewRecorder()
			tt.reply(want, httptest.NewRequest("GET", "/x", nil))
			got := httptest.NewRecorder()
			problemHandler(fakeMux{tt.reply}).ServeHTTP(got, httptest.NewRequest("GET", "/x", nil))

			if got.Code != want.Code || !maps.EqualFunc(got.Header(), want.Header(), slices.Equal) || got.Body.String() != want.Body.String() {
				t.Errorf("reply = %d %v %q, want %d %v %q", got.Code, got.Header(), got.Body, want.Code, want.Header(), want.Body)
			}
		})
	}
}

func TestProblemHandlerProblems(t *testing.T) {
	tests := []struct {
		status int
		allow  string
		want   string
	}{
		{http.StatusNotFound, "", `{"type":"about:blank","title":"Not Found","status":404}`},
		{http.StatusMethodNotAllowed, "PUT", `{"type":"about:blank","title":"Method Not Allowed","status":405}`},
	}
	for _, tt := range tests {
		reply := func(w http.ResponseWriter, r *http.Request) {
			if tt.allow != "" {
				w.Header().Set("Allow", tt.allow)
			}
			http.Error(w, http.StatusText(tt.status), tt.status)
		}
		rec := httptest.NewRecorder()
		problemHandler(fakeMux{reply}).ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))

		if rec.Code != tt.status || rec.Header().Get("Content-Type") != "application/problem+json" || rec.Header().Get("Allow") != tt.allow || rec.Body.String() != tt.want {
			t.Errorf("reply %d = %d %v %s, want the problem %s with Allow %q", tt.status, rec.Code, rec.Header(), rec.Body, tt.want, tt.allow)
		}
	}
}

func TestProblemHandlerOneProblem(t *testing.T) {
	reply := func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
		http.NotFound(w, r)
	}
	rec := httptest.NewRecorder()
	problemHandler(fakeMux{reply}).ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
	if want := `{"type":"about:blank","title":"Not Found","status":404}`; rec.Body.String() != want {
		t.Errorf("reply 404 twice = %s, want one problem %s", rec.Body, want)
	}
}

func TestProblemHandlerUnwrap(t *testing.T) {
	var err error
	reply := func(w http.ResponseWriter, r *http.Request) {
		err = http.NewResponseController(w).Flush()
	}
	rec := httptest.NewRecorder()
	problemHandler(fakeMux{reply}).ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
	if err != nil || !rec.Flushed {
		t.Errorf("Flush() error = %v, flushed: %t; want the writer under the problemWriter flushed", err, rec.Flushed)
	}
}
