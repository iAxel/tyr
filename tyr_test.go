package tyr_test

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/iaxel/tyr"
)

type getLinkReq struct {
	Code string `json:"code"`
}

type link struct {
	Code string `json:"code"`
	URL  string `json:"url"`
}

func getLink(ctx context.Context, req getLinkReq) (*link, error) {
	return &link{Code: req.Code, URL: "https://go.dev/" + req.Code}, nil
}

type linkService struct{}

func (linkService) Get(ctx context.Context, req getLinkReq) (*link, error) {
	return getLink(ctx, req)
}

func TestHandle(t *testing.T) {
	api := tyr.New()
	ops := []*tyr.Operation{
		api.Handle("links.get", getLink),
		api.Handle("links.method", linkService{}.Get),
		api.Handle("links.closure", func(ctx context.Context, req getLinkReq) (string, error) {
			return req.Code, nil
		}),
	}

	want := []struct {
		name     string
		req, res reflect.Type
	}{
		{"links.get", reflect.TypeFor[getLinkReq](), reflect.TypeFor[*link]()},
		{"links.method", reflect.TypeFor[getLinkReq](), reflect.TypeFor[*link]()},
		{"links.closure", reflect.TypeFor[getLinkReq](), reflect.TypeFor[string]()},
	}
	for i, op := range ops {
		if op.Name() != want[i].name || op.Req() != want[i].req || op.Res() != want[i].res {
			t.Errorf("operation %d = %s(%v) %v, want %s(%v) %v",
				i, op.Name(), op.Req(), op.Res(), want[i].name, want[i].req, want[i].res)
		}
	}
	if got := slices.Collect(api.Operations()); !slices.Equal(got, ops) {
		t.Errorf("Operations() = %v, want %v", names(got), names(ops))
	}
}

func TestHandleNames(t *testing.T) {
	const (
		invalid  = "name must be dot-separated segments of ASCII letters, digits, '_' and '-'"
		reserved = `the first segment "rpc" is reserved by JSON-RPC`
	)
	tests := []struct {
		name string
		want string // what's wrong with the name, or "" if it's valid
	}{
		{"links", ""},
		{"links.get", ""},
		{"v2.links.get_by-code", ""},
		{"rpcx.status", ""},
		{"RPC.status", ""}, // JSON-RPC reserves only the lowercase prefix
		{"", invalid},
		{".links", invalid},
		{"links.", invalid},
		{"links..get", invalid},
		{"links get", invalid},
		{"links/get", invalid},
		{"ссылки.get", invalid},
		{"rpc", reserved},
		{"rpc.discover", reserved},
	}
	for _, tt := range tests {
		var want any
		if tt.want != "" {
			want = fmt.Sprintf("tyr: Handle(%q): %s", tt.name, tt.want)
		}
		if got := panicValue(func() { tyr.New().Handle(tt.name, getLink) }); got != want {
			t.Errorf("Handle(%q) panicked with %v, want %v", tt.name, got, want)
		}
	}
}

func TestHandlePanics(t *testing.T) {
	tests := []struct {
		name     string
		register func(api *tyr.API)
		want     string
	}{
		{
			name: "duplicate name",
			register: func(api *tyr.API) {
				api.Handle("links.get", getLink)
				api.Group().Handle("links.get", getLink)
			},
			want: `tyr: Handle("links.get"): duplicate operation name`,
		},
		{
			name:     "nil handler",
			register: func(api *tyr.API) { api.Handle("links.get", tyr.Handler[getLinkReq, *link](nil)) },
			want:     `tyr: Handle("links.get"): nil handler`,
		},
		{
			name: "pointer request",
			register: func(api *tyr.API) {
				api.Handle("links.get", func(ctx context.Context, req *getLinkReq) (*link, error) { return nil, nil })
			},
			want: `tyr: Handle("links.get"): request type *tyr_test.getLinkReq is not a struct; use tyr_test.getLinkReq`,
		},
		{
			name: "non-struct request",
			register: func(api *tyr.API) {
				api.Handle("links.get", func(ctx context.Context, code string) (*link, error) { return nil, nil })
			},
			want: `tyr: Handle("links.get"): request type string is not a struct`,
		},
		{
			name:     "nil option",
			register: func(api *tyr.API) { api.Handle("links.get", getLink, nil) },
			want:     `tyr: Handle("links.get"): nil option`,
		},
		{
			name:     "nil group option",
			register: func(api *tyr.API) { api.Group(nil).Handle("links.get", getLink) },
			want:     `tyr: Handle("links.get"): nil option`,
		},
		{
			name:     "nil mapper",
			register: func(api *tyr.API) { api.MapError(nil) },
			want:     "tyr: MapError: nil mapper",
		},
		{
			name:     "nil interceptor",
			register: func(api *tyr.API) { api.Use(passThrough, nil) },
			want:     "tyr: Use: nil interceptor",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := panicValue(func() { tt.register(tyr.New()) }); got != tt.want {
				t.Errorf("panicked with %v, want %q", got, tt.want)
			}
		})
	}
}

func TestGroup(t *testing.T) {
	var applied []string
	option := func(tag string) tyr.OpOption {
		return func(op *tyr.Operation) { applied = append(applied, op.Name()+" "+tag) }
	}

	api := tyr.New()
	opts := []tyr.OpOption{option("admin")}
	admin := api.Group(opts...)
	opts[0] = option("changed") // the group keeps its own copy
	admin.Group(option("audit")).Handle("links.purge", getLink, option("own"))
	admin.Group(option("beta")).Handle("links.stats", getLink)
	admin.Handle("links.list", getLink)
	api.Handle("links.get", getLink, option("own"))

	want := []string{
		"links.purge admin", "links.purge audit", "links.purge own",
		"links.stats admin", "links.stats beta",
		"links.list admin",
		"links.get own",
	}
	if !slices.Equal(applied, want) {
		t.Errorf("applied options:\n%q\nwant:\n%q", applied, want)
	}
	wantOps := []string{"links.purge", "links.stats", "links.list", "links.get"}
	if got := names(slices.Collect(api.Operations())); !slices.Equal(got, wantOps) {
		t.Errorf("Operations() = %v, want %v", got, wantOps)
	}
}

func TestSeal(t *testing.T) {
	api := tyr.New()
	get := api.Handle("links.get", getLink)

	// Operations is a plain read, so registering after it is fine.
	_ = slices.Collect(api.Operations())
	api.Handle("links.list", getLink)

	api.Seal()
	api.Seal() // sealing again does nothing

	const after = " after Seal: register everything before mounting the API"
	tests := []struct {
		name string
		call func()
		want string
	}{
		{"Handle", func() { api.Handle("links.late", getLink) }, `tyr: Handle("links.late")` + after},
		{"Group.Handle", func() { api.Group().Handle("links.late", getLink) }, `tyr: Handle("links.late")` + after},
		{"MapError", func() { api.MapError(func(err error) error { return err }) }, "tyr: MapError" + after},
		{"Use", func() { api.Use(passThrough) }, "tyr: Use" + after},
	}
	for _, tt := range tests {
		if got := panicValue(tt.call); got != tt.want {
			t.Errorf("%s on a sealed API panicked with %v, want %q", tt.name, got, tt.want)
		}
	}

	// A sealed API still lists and runs its operations.
	wantOps := []string{"links.get", "links.list"}
	if got := names(slices.Collect(api.Operations())); !slices.Equal(got, wantOps) {
		t.Errorf("Operations() = %v, want %v", got, wantOps)
	}
	if _, err := get.Call(t.Context(), nil); err != nil {
		t.Errorf("Call() on a sealed API = %v, want <nil>", err)
	}
}

// names returns the names of ops.
func names(ops []*tyr.Operation) []string {
	var s []string
	for _, op := range ops {
		s = append(s, op.Name())
	}
	return s
}

// panicValue calls f and returns the value it panicked with, or nil.
func panicValue(f func()) (v any) {
	defer func() { v = recover() }()
	f()
	return nil
}
