package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/iaxel/tyr"
	"github.com/iaxel/tyr/examples/shortlink/authz"
	"github.com/iaxel/tyr/examples/shortlink/links"
	"github.com/iaxel/tyr/examples/shortlink/store"
)

// adminToken is the token of the admin of the service under test.
const adminToken = "secret"

// service is the service under test.
type service struct {
	api    *tyr.API
	client *http.Client
	logs   *logs
}

// start starts the service on an in-memory test server. It runs in a
// synctest bubble, whose clock dates links 2000-01-01.
func start(t *testing.T) *service {
	logs := &logs{}
	logger := slog.New(tyr.NewLogHandler(logs))
	api := newAPI(links.New(store.New()), logger)
	callers := map[string]authz.Caller{adminToken: {Name: "admin", Roles: []string{"admin"}}}
	srv := newServer("", api, callers, logger)
	return &service{api: api, client: httptest.NewTestServer(t, srv.Handler).Client(), logs: logs}
}

// do sends a request, with a JSON body unless body is empty and with the
// header given as pairs of names and values, and returns the response and
// its body.
func (s *service) do(t *testing.T, method, target, body string, header ...string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, "http://shortlink.example"+target, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for kv := range slices.Chunk(header, 2) {
		req.Header.Set(kv[0], kv[1])
	}
	resp, err := s.client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("%s %s: reading the body: %v", method, target, err)
	}
	return resp, string(data)
}

func TestLinks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := start(t)
		const link = `{"code":"go-docs","url":"https://go.dev/doc/","created_at":"2000-01-01T00:00:00Z"}`

		resp, body := s.do(t, "POST", "/links", `{"url":"https://go.dev/doc/","code":"go-docs"}`)
		if resp.StatusCode != http.StatusCreated || body != link || resp.Header.Get("X-Request-ID") == "" {
			t.Errorf("create = %d %s, request ID %q; want 201 %s with an ID", resp.StatusCode, body, resp.Header.Get("X-Request-ID"), link)
		}
		resp, body = s.do(t, "GET", "/links/go-docs", "")
		if resp.StatusCode != http.StatusOK || body != link {
			t.Errorf("get = %d %s, want 200 %s", resp.StatusCode, body, link)
		}

		// The errors of the store, translated by MapError.
		resp, body = s.do(t, "POST", "/links", `{"url":"https://go.dev","code":"go-docs"}`)
		const taken = `{"type":"about:blank","title":"Conflict","status":409,"detail":"code is taken","kind":"already_exists"}`
		if resp.StatusCode != http.StatusConflict || body != taken {
			t.Errorf("create again = %d %s, want 409 %s", resp.StatusCode, body, taken)
		}
		resp, body = s.do(t, "GET", "/links/nope", "")
		const notFound = `{"type":"about:blank","title":"Not Found","status":404,"detail":"link not found","kind":"not_found"}`
		if resp.StatusCode != http.StatusNotFound || body != notFound {
			t.Errorf("get a missing link = %d %s, want 404 %s", resp.StatusCode, body, notFound)
		}
	})
}

func TestRandomCode(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := start(t)
		resp, body := s.do(t, "POST", "/links", `{"url":"https://go.dev"}`)
		code := regexp.MustCompile(`^\{"code":"([a-z2-7]{7})","url":"https://go.dev",`).FindStringSubmatch(body)
		if resp.StatusCode != http.StatusCreated || code == nil {
			t.Fatalf("create = %d %s, want 201 and a random code", resp.StatusCode, body)
		}
		if resp, body := s.do(t, "GET", "/links/"+code[1], ""); resp.StatusCode != http.StatusOK {
			t.Errorf("get = %d %s, want 200", resp.StatusCode, body)
		}
	})
}

func TestCreateInvalid(t *testing.T) {
	violation := func(pointer, detail string) string {
		return `{"type":"about:blank","title":"Bad Request","status":400,"detail":"validation failed",` +
			`"kind":"invalid_argument","errors":[{"pointer":"` + pointer + `","detail":"` + detail + `"}]}`
	}
	tests := []struct {
		name   string
		body   string
		header []string
		status int
		want   string
	}{
		{
			name:   "no URL",
			body:   `{"code":"go-docs"}`,
			status: http.StatusBadRequest,
			want:   violation("/url", "is required"),
		},
		{
			name:   "not a URL",
			body:   `{"url":"go.dev"}`,
			status: http.StatusBadRequest,
			want:   violation("/url", "must be a URL"),
		},
		{
			name:   "not an http URL",
			body:   `{"url":"javascript:alert(1)"}`,
			status: http.StatusBadRequest,
			want:   violation("/url", "must be an http or https URL"),
		},
		{
			name:   "short code",
			body:   `{"url":"https://go.dev","code":"go"}`,
			status: http.StatusBadRequest,
			want:   violation("/code", "must be at least 4 characters"),
		},
		{
			name:   "characters of the code",
			body:   `{"url":"https://go.dev","code":"Go_Dev"}`,
			status: http.StatusBadRequest,
			want:   violation("/code", "only a-z, 0-9 and '-'"),
		},
		{
			name:   "broken JSON",
			body:   `{"url":}`,
			status: http.StatusBadRequest,
			want:   violation("", "invalid JSON at byte offset 7: invalid character '}' at start of value"),
		},
		{
			name:   "not JSON",
			body:   `url=https://go.dev`,
			header: []string{"Content-Type", "application/x-www-form-urlencoded"},
			status: http.StatusUnsupportedMediaType,
			want:   `{"type":"about:blank","title":"Unsupported Media Type","status":415,"detail":"request body must be JSON: application/json or a +json type"}`,
		},
		{
			name:   "too large",
			body:   `{"url":"https://go.dev/?q=` + strings.Repeat("a", 1<<20) + `"}`,
			status: http.StatusRequestEntityTooLarge,
			want:   `{"type":"about:blank","title":"Request Entity Too Large","status":413,"detail":"request body is larger than 1048576 bytes"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				resp, body := start(t).do(t, "POST", "/links", tt.body, tt.header...)
				if resp.StatusCode != tt.status || body != tt.want {
					t.Errorf("create = %d %s, want %d %s", resp.StatusCode, body, tt.status, tt.want)
				}
			})
		})
	}
}

func TestCrossOrigin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := start(t)
		// A page of another site posts to the service from a browser.
		if resp, body := s.do(t, "POST", "/links", `{"url":"https://evil.example"}`, "Sec-Fetch-Site", "cross-site"); resp.StatusCode != http.StatusForbidden {
			t.Errorf("cross-site create = %d %s, want 403", resp.StatusCode, body)
		}
		if resp, body := s.do(t, "POST", "/links", `{"url":"https://go.dev"}`, "Sec-Fetch-Site", "same-origin"); resp.StatusCode != http.StatusCreated {
			t.Errorf("same-origin create = %d %s, want 201", resp.StatusCode, body)
		}
	})
}

// Not parallel: it replaces the default logger, which handlers log to.
func TestLogs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := start(t)
		defer slog.SetDefault(slog.Default())
		slog.SetDefault(slog.New(tyr.NewLogHandler(s.logs)))

		// With a token: then Authenticate passes on a request with another
		// context, and the access record still has the route.
		resp, _ := s.do(t, "POST", "/links", `{"url":"https://go.dev","code":"golang"}`, "Authorization", "Bearer "+adminToken)
		synctest.Wait()

		// The record of the handler and the access record have the ID of the
		// request, sent back to the client.
		id := resp.Header.Get("X-Request-ID")
		want := []record{
			{msg: "link created", attrs: map[string]string{"request_id": id, "operation": "links.create", "code": "golang"}},
			{msg: "middleware: request", attrs: map[string]string{
				"request_id": id, "method": "POST", "route": "POST /links", "status": "201", "duration": "0s",
			}},
		}
		if got := s.logs.get(); !slices.EqualFunc(got, want, equalRecords) {
			t.Errorf("logged %+v, want %+v", got, want)
		}
	})
}

func TestPurge(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := start(t)
		for _, body := range []string{
			`{"url":"https://evil.example/a","code":"evil-a"}`,
			`{"url":"http://EVIL.example:8080/b","code":"evil-b"}`,
			`{"url":"https://go.dev","code":"go-dev"}`,
		} {
			if resp, body := s.do(t, "POST", "/links", body); resp.StatusCode != http.StatusCreated {
				t.Fatalf("create = %d %s", resp.StatusCode, body)
			}
		}

		// Purge has no REST route, and M2 has no JSON-RPC to serve it: call
		// it in-process, as a transport would.
		purge := operation(t, s.api, "links.purge")
		call := func(ctx context.Context) (any, error) {
			return purge.Call(ctx, func(dst any) error {
				*dst.(*links.PurgeReq) = links.PurgeReq{Host: "evil.example"}
				return nil
			})
		}
		ctx := t.Context()
		if _, err := call(ctx); !hasKind(err, tyr.KindUnauthenticated) {
			t.Errorf("purge without a caller: error = %v, want unauthenticated", err)
		}
		user := authz.WithCaller(ctx, authz.Caller{Name: "user"})
		if _, err := call(user); !hasKind(err, tyr.KindPermissionDenied) {
			t.Errorf("purge by a user: error = %v, want permission_denied", err)
		}
		admin := authz.WithCaller(ctx, authz.Caller{Name: "admin", Roles: []string{"admin"}})
		if res, err := call(admin); err != nil || res != (links.PurgeRes{Purged: 2}) {
			t.Errorf("purge by the admin = %v, %v; want 2 purged", res, err)
		}

		for code, status := range map[string]int{"evil-a": http.StatusNotFound, "evil-b": http.StatusNotFound, "go-dev": http.StatusOK} {
			if resp, body := s.do(t, "GET", "/links/"+code, ""); resp.StatusCode != status {
				t.Errorf("get %s = %d %s, want %d", code, resp.StatusCode, body, status)
			}
		}
	})
}

func TestShutdown(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	srv := newServer("", newAPI(links.New(store.New()), logger), nil, logger)
	// The signal comes from the handler: net/http drops a request it has
	// read but not yet handled when Shutdown starts, so ConnState's
	// StateActive would be too early.
	handling := make(chan struct{})
	handler := srv.Handler
	srv.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(handling)
		handler.ServeHTTP(w, r)
	})
	stopping := make(chan struct{})
	srv.RegisterOnShutdown(func() { close(stopping) })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() { served <- serve(ctx, srv, ln) }()

	// A request is in flight, half of its body sent, when the service is
	// told to stop.
	body, w := io.Pipe()
	req, err := http.NewRequest("POST", "http://"+ln.Addr().String()+"/links", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Transport: &http.Transport{}}
	responded := make(chan *http.Response, 1)
	go func() {
		resp, err := client.Do(req)
		if err != nil {
			t.Errorf("create: %v", err)
		}
		responded <- resp
	}()
	_, _ = io.WriteString(w, `{"url":`)
	<-handling
	stop()
	<-stopping

	// The service finishes the request before it stops.
	_, _ = io.WriteString(w, `"https://go.dev"}`)
	_ = w.Close()
	if resp := <-responded; resp == nil || resp.StatusCode != http.StatusCreated {
		t.Errorf("create = %v, want 201", resp)
	} else {
		_ = resp.Body.Close()
	}
	if err := <-served; err != nil {
		t.Errorf("serve() = %v, want <nil>", err)
	}
	if _, err := net.Dial("tcp", ln.Addr().String()); err == nil {
		t.Error("the service still accepts connections")
	}
}

// operation returns the operation of api with the name.
func operation(t *testing.T, api *tyr.API, name string) *tyr.Operation {
	t.Helper()
	for op := range api.Operations() {
		if op.Name() == name {
			return op
		}
	}
	t.Fatalf("no operation %s", name)
	return nil
}

// hasKind reports whether err is a *tyr.Error of the kind k.
func hasKind(err error, k tyr.Kind) bool {
	e, ok := errors.AsType[*tyr.Error](err)
	return ok && e.Kind == k
}

// logs is a slog.Handler that keeps the records it gets.
type logs struct {
	mu      sync.Mutex
	records []record
}

// record is a record that logs got, with its attributes as text.
type record struct {
	msg   string
	attrs map[string]string
}

func (l *logs) Enabled(context.Context, slog.Level) bool {
	return true
}

func (l *logs) Handle(_ context.Context, r slog.Record) error {
	rec := record{msg: r.Message, attrs: make(map[string]string)}
	r.Attrs(func(a slog.Attr) bool {
		rec.attrs[a.Key] = a.Value.String()
		return true
	})
	l.mu.Lock()
	defer l.mu.Unlock()
	l.records = append(l.records, rec)
	return nil
}

func (l *logs) WithAttrs([]slog.Attr) slog.Handler {
	return l
}

func (l *logs) WithGroup(string) slog.Handler {
	return l
}

// get returns the records l got so far.
func (l *logs) get() []record {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.records)
}

// equalRecords reports whether the records a and b are equal.
func equalRecords(a, b record) bool {
	return a.msg == b.msg && maps.Equal(a.attrs, b.attrs)
}
