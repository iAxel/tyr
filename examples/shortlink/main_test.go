package main

import (
	"context"
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

// The tokens of the admin and of a user of the service under test.
const (
	adminToken = "secret"
	userToken  = "user-secret"
)

// service is the service under test.
type service struct {
	client *http.Client
	logs   *logs
}

// start starts the service on an in-memory test server. It runs in a
// synctest bubble, whose clock dates links 2000-01-01.
func start(t *testing.T) *service {
	logs := &logs{}
	logger := slog.New(tyr.NewLogHandler(logs))
	api := newAPI(links.New(store.New()), logger)
	callers := map[string]authz.Caller{
		adminToken: {Name: "admin", Roles: []string{"admin"}},
		userToken:  {Name: "user"},
	}
	srv := newServer("", api, callers, logger)
	client := httptest.NewTestServer(t, srv.Handler).Client()
	// Show redirects to the test instead of following them: the client of
	// the test server sends requests to every host to the service.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &service{client: client, logs: logs}
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
		if resp.StatusCode != http.StatusCreated || body != link || resp.Header.Get("Location") != "/links/go-docs" || resp.Header.Get("X-Request-ID") == "" {
			t.Errorf("create = %d %v %s; want 201 %s with a Location and a request ID", resp.StatusCode, resp.Header, body, link)
		}
		resp, body = s.do(t, "GET", "/links/go-docs", "")
		if resp.StatusCode != http.StatusOK || body != link {
			t.Errorf("get = %d %s, want 200 %s", resp.StatusCode, body, link)
		}
		// The short link itself redirects.
		resp, body = s.do(t, "GET", "/go-docs", "")
		if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "https://go.dev/doc/" || body != "" {
			t.Errorf("follow = %d %v %q, want 302 to https://go.dev/doc/ without a body", resp.StatusCode, resp.Header, body)
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
			want:   violation("/url", "must be an http or https URL"),
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
		const forbidden = `{"type":"about:blank","title":"Forbidden","status":403}`
		if resp, body := s.do(t, "POST", "/links", `{"url":"https://evil.example"}`, "Sec-Fetch-Site", "cross-site"); resp.StatusCode != http.StatusForbidden || body != forbidden {
			t.Errorf("cross-site create = %d %s, want 403 %s", resp.StatusCode, body, forbidden)
		}
		if resp, body := s.do(t, "POST", "/links", `{"url":"https://go.dev"}`, "Sec-Fetch-Site", "same-origin"); resp.StatusCode != http.StatusCreated {
			t.Errorf("same-origin create = %d %s, want 201", resp.StatusCode, body)
		}
	})
}

func TestDelete(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := start(t)
		if resp, body := s.do(t, "POST", "/links", `{"url":"https://go.dev","code":"golang"}`); resp.StatusCode != http.StatusCreated {
			t.Fatalf("create = %d %s", resp.StatusCode, body)
		}

		// Only the admin deletes links; a client without a token learns how
		// to authenticate.
		resp, body := s.do(t, "DELETE", "/links/golang", "")
		const unauthenticated = `{"type":"about:blank","title":"Unauthorized","status":401,"detail":"a valid bearer token is required","kind":"unauthenticated"}`
		if resp.StatusCode != http.StatusUnauthorized || resp.Header.Get("WWW-Authenticate") != `Bearer realm="shortlink"` || body != unauthenticated {
			t.Errorf("delete without a token = %d %v %s, want 401 with a challenge", resp.StatusCode, resp.Header, body)
		}
		if resp, body := s.do(t, "DELETE", "/links/golang", "", "Authorization", "Bearer "+userToken); resp.StatusCode != http.StatusForbidden {
			t.Errorf("delete by a user = %d %s, want 403", resp.StatusCode, body)
		}
		if resp, body := s.do(t, "DELETE", "/links/golang", "", "Authorization", "Bearer "+adminToken); resp.StatusCode != http.StatusNoContent || body != "" {
			t.Errorf("delete by the admin = %d %q, want 204", resp.StatusCode, body)
		}
		if resp, _ := s.do(t, "GET", "/links/golang", ""); resp.StatusCode != http.StatusNotFound {
			t.Errorf("get a deleted link = %d, want 404", resp.StatusCode)
		}
		if resp, _ := s.do(t, "DELETE", "/links/golang", "", "Authorization", "Bearer "+adminToken); resp.StatusCode != http.StatusNotFound {
			t.Errorf("delete a deleted link = %d, want 404", resp.StatusCode)
		}
	})
}

func TestMuxProblems(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := start(t)
		// The mux has no route for the path, or none for the method.
		resp, body := s.do(t, "GET", "/links/go/stats", "")
		const notFound = `{"type":"about:blank","title":"Not Found","status":404}`
		if resp.StatusCode != http.StatusNotFound || resp.Header.Get("Content-Type") != "application/problem+json" || body != notFound {
			t.Errorf("unknown path = %d %v %s, want 404 %s", resp.StatusCode, resp.Header, body, notFound)
		}
		resp, body = s.do(t, "PUT", "/links/go", "")
		const notAllowed = `{"type":"about:blank","title":"Method Not Allowed","status":405}`
		if resp.StatusCode != http.StatusMethodNotAllowed || resp.Header.Get("Allow") != "DELETE, GET, HEAD" || body != notAllowed {
			t.Errorf("PUT = %d %v %s, want 405 %s with Allow", resp.StatusCode, resp.Header, body, notAllowed)
		}
	})
}

// Not parallel: it replaces the default logger, which handlers log to.
func TestLogs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := start(t)
		defer slog.SetDefault(slog.Default())
		slog.SetDefault(slog.New(tyr.NewLogHandler(s.logs)))

		// With a token: then Authenticate, under Logger, passes on a request
		// with another context, and the access record still has the route
		// and the operation, which rest records.
		resp, _ := s.do(t, "POST", "/links", `{"url":"https://go.dev","code":"golang"}`, "Authorization", "Bearer "+adminToken)
		synctest.Wait()

		// The record of the handler and the access record have the ID of the
		// request, sent back to the client.
		id := resp.Header.Get("X-Request-ID")
		want := []record{
			{msg: "link created", attrs: map[string]string{"request_id": id, "operation": "links.create", "code": "golang"}},
			{msg: "middleware: request", attrs: map[string]string{
				"request_id": id, "method": "POST", "route": "POST /links", "operation": "links.create",
				"status": "201", "duration": "0s",
			}},
		}
		if got := s.logs.get(); !slices.EqualFunc(got, want, equalRecords) {
			t.Errorf("logged %+v, want %+v", got, want)
		}
	})
}

func TestRPC(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := start(t)
		const link = `{"code":"go-docs","url":"https://go.dev/doc/","created_at":"2000-01-01T00:00:00Z"}`

		// The operations of REST, by name, with the same store.
		tests := []struct {
			name string
			call string
			want string
		}{
			{
				name: "create",
				call: `{"jsonrpc":"2.0","method":"links.create","params":{"url":"https://go.dev/doc/","code":"go-docs"},"id":1}`,
				want: `{"jsonrpc":"2.0","result":` + link + `,"id":1}`,
			},
			{
				// REST redirects to the link, JSON-RPC returns it.
				name: "follow",
				call: `{"jsonrpc":"2.0","method":"links.follow","params":{"code":"go-docs"},"id":2}`,
				want: `{"jsonrpc":"2.0","result":{"url":"https://go.dev/doc/"},"id":2}`,
			},
			{
				name: "batch",
				call: `[{"jsonrpc":"2.0","method":"links.get","params":{"code":"go-docs"},"id":3},` +
					`{"jsonrpc":"2.0","method":"links.get","params":{"code":"nope"},"id":4}]`,
				want: `[{"jsonrpc":"2.0","result":` + link + `,"id":3},` +
					`{"jsonrpc":"2.0","error":{"code":404,"message":"link not found","data":{"kind":"not_found"}},"id":4}]`,
			},
		}
		for _, tt := range tests {
			if resp, body := s.do(t, "POST", "/rpc", tt.call); resp.StatusCode != http.StatusOK || body != tt.want {
				t.Errorf("%s = %d %s, want 200 %s", tt.name, resp.StatusCode, body, tt.want)
			}
		}
		if resp, body := s.do(t, "GET", "/links/go-docs", ""); resp.StatusCode != http.StatusOK || body != link {
			t.Errorf("get over REST = %d %s, want 200 %s", resp.StatusCode, body, link)
		}
	})
}

func TestRPCLogs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := start(t)
		resp, _ := s.do(t, "POST", "/rpc", `{"jsonrpc":"2.0","method":"links.get","params":{"code":"nope"},"id":1}`)
		synctest.Wait()

		// Every call has the route of the endpoint; the access record has
		// the operation too, which jsonrpc records.
		want := []record{{msg: "middleware: request", attrs: map[string]string{
			"request_id": resp.Header.Get("X-Request-ID"), "method": "POST", "route": "POST /rpc", "operation": "links.get",
			"status": "200", "duration": "0s",
		}}}
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

		// Purge has no REST route: JSON-RPC serves it, and the interceptor
		// authorizes it as it would over REST.
		const purge = `{"jsonrpc":"2.0","method":"links.purge","params":{"host":"evil.example"},"id":1}`
		tests := []struct {
			name  string
			token string
			want  string
		}{
			{"without a token", "", `{"jsonrpc":"2.0","error":{"code":401,"message":"a valid bearer token is required","data":{"kind":"unauthenticated"}},"id":1}`},
			{"by a user", userToken, `{"jsonrpc":"2.0","error":{"code":403,"message":"links.purge requires the role admin","data":{"kind":"permission_denied"}},"id":1}`},
			{"by the admin", adminToken, `{"jsonrpc":"2.0","result":{"purged":2},"id":1}`},
		}
		for _, tt := range tests {
			var header []string
			if tt.token != "" {
				header = []string{"Authorization", "Bearer " + tt.token}
			}
			if resp, body := s.do(t, "POST", "/rpc", purge, header...); resp.StatusCode != http.StatusOK || body != tt.want {
				t.Errorf("purge %s = %d %s, want 200 %s", tt.name, resp.StatusCode, body, tt.want)
			}
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
