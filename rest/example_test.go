package rest_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/iaxel/tyr"
	"github.com/iaxel/tyr/rest"
)

func ExampleMount() {
	type GetLinkReq struct {
		Code string `json:"code" path:"code"`
	}

	api := tyr.New()
	api.Handle("links.get", func(ctx context.Context, req GetLinkReq) (string, error) {
		if req.Code != "go" {
			return "", tyr.NotFound("link %q not found", req.Code)
		}
		return "https://go.dev", nil
	}, rest.Route("GET /links/{code}"))

	mux := http.NewServeMux()
	rest.Mount(mux, api)

	for _, target := range []string{"/links/go", "/links/rust"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", target, nil))
		fmt.Println(rec.Code, rec.Body)
	}
	// Output:
	// 200 "https://go.dev"
	// 404 {"type":"about:blank","title":"Not Found","status":404,"detail":"link \"rust\" not found","kind":"not_found"}
}
