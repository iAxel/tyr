# ᛏ tyr

```go
type GetReq struct {
	Code string `json:"code" path:"code" validate:"required"`
}

func Get(ctx context.Context, req GetReq) (*Link, error) { /* ... */ }

func main() {
	api := tyr.New()
	api.Handle("links.get", Get, rest.Route("GET /links/{code}"))

	mux := http.NewServeMux()
	rest.Mount(mux, api)
	// v0.2.0: mux.Handle("POST /rpc", jsonrpc.Handler(api))

	log.Fatal(http.ListenAndServe(":8080", mux))
}
```

tyr serves typed operations over `net/http`. A handler is a plain function, `func(ctx, Req) (Res, error)`, with no HTTP types and no context of its own. tyr decodes the request (the JSON body, then the fields tagged `path`, `query` or `header`), validates it, calls the handler and encodes the result or the error. The operation has a name, `links.get`: REST serves it at its route, and JSON-RPC, in v0.2.0, will serve the same operation by name.

## Coming from NestJS, Hono or Go

| tyr | NestJS / Hono | What a Gopher already knows |
|---|---|---|
| `func(ctx, Req) (Res, error)` | controller method returning a value | gRPC service method, same shape |
| `validate:"required,min=4"` | DTO + class-validator | go-playground/validator, Gin's `binding` |
| `Interceptor` + `MetaKey` | Interceptor, Guard + decorator | gRPC interceptor, context key |
| `MapError` | ExceptionFilter, `app.onError` | Echo's `HTTPErrorHandler` |
| `Define` + client (v0.3) | tRPC, Hono RPC, Eden | gRPC proto contract, without codegen |

## Errors

A handler returns a `*tyr.Error` of a kind, and REST sends it as RFC 9457 `application/problem+json` with the status of the kind:

```go
return nil, tyr.NotFound("link %q not found", req.Code)
```

```json
{"type":"about:blank","title":"Not Found","status":404,"detail":"link \"x\" not found","kind":"not_found"}
```

Errors of other packages, such as a store's, need no tyr import there: `MapError` translates them. Anything left untranslated reaches the client as an internal error and goes to the log, with its cause. The client learns no more of an internal error than `"internal error"`: the message of a `tyr.Internal` goes to the log too.

```go
api.MapError(func(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return tyr.NotFound("link not found").WithCause(err)
	}
	return err
})
```

## Validation

`validate` tags use the syntax of go-playground/validator. The core implements a subset, with the semantics of v10.30.5: `required`, `omitempty`, `min`, `max`, `len`, `gt`, `gte`, `lt`, `lte`, `oneof`, `email`, `url`, `http_url`, `uuid`. An unknown rule panics at startup. Rules that tags can't express go in a `Validate` method:

```go
type CreateReq struct {
	URL  string `json:"url" validate:"required,http_url"`
	Code string `json:"code" validate:"omitempty,min=4,max=16"`
}

func (r CreateReq) Validate() error {
	var v tyr.Violations
	if r.Code != "" && !codeRe.MatchString(r.Code) {
		v.Add("code", "only a-z, 0-9 and '-'")
	}
	return v.Err()
}
```

A failed check is a 400 with a JSON pointer per field:

```json
{"type":"about:blank","title":"Bad Request","status":400,"detail":"validation failed","kind":"invalid_argument","errors":[{"pointer":"/code","detail":"must be at least 4 characters"}]}
```

## Interceptors and metadata

Interceptors run around every operation, over every transport, so authorization belongs there rather than in the middleware of a route. A `MetaKey` attaches typed metadata to operations, for interceptors to read. Who the caller is comes from the HTTP request, so middleware finds that out and puts the caller in the context, under a typed key of `ctxkey`:

```go
var callerKey = ctxkey.New[Caller]("caller")

// authenticate is HTTP middleware: it reads the request.
func authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, ok := callerOf(r.Header.Get("Authorization")); ok {
			r = r.WithContext(callerKey.Set(r.Context(), c))
		}
		next.ServeHTTP(w, r)
	})
}

var roleKey = tyr.NewMetaKey[string]("role")

// The interceptor authorizes, over every transport.
api.Use(func(ctx context.Context, op *tyr.Operation, req any, next tyr.Invoker) (any, error) {
	role, ok := roleKey.Get(op)
	if !ok {
		return next(ctx, req)
	}
	c, ok := callerKey.Get(ctx)
	if !ok {
		return nil, tyr.Unauthenticated("log in first")
	}
	if !slices.Contains(c.Roles, role) {
		return nil, tyr.PermissionDenied("%s requires the role %s", op.Name(), role)
	}
	return next(ctx, req)
})

admin := api.Group(roleKey.Option("admin"))
admin.Handle("links.delete", Delete, rest.Route("DELETE /links/{code}"))

// RFC 9110 requires a WWW-Authenticate challenge on every 401.
rest.Mount(mux, api, rest.Challenge(`Bearer realm="links"`))
```

## Headers and redirects

A result sets headers of the response with fields tagged `header`, and a redirect is a status and a `Location`:

```go
type FollowRes struct {
	URL string `json:"url" header:"Location"` // JSON-RPC gets it as a member
}

api.Handle("links.follow", Follow, rest.Route("GET /{code}"), rest.Status(http.StatusFound))
```

## Middleware and logs

HTTP middleware is `func(http.Handler) http.Handler`, and `middleware.Chain` applies it, the first outermost. `rest.ProblemHandler` makes the 404 and 405 of the mux problems too:

```go
slog.SetDefault(slog.New(tyr.NewLogHandler(slog.NewJSONHandler(os.Stderr, nil))))

handler := middleware.Chain(rest.ProblemHandler(mux),
	middleware.RequestID(),
	middleware.Logger(slog.Default()),
	middleware.Recover(slog.Default()),
	http.NewCrossOriginProtection().Handler,
	authenticate,
)
```

Logger writes a record per request with its route and operation, which the transport records in a `tyr.RequestInfo` in the request context. So middleware under Logger may pass on another request, as `authenticate` does to put the caller in the context. Metrics of your own read the same `RequestInfo` once the handler returns:

```go
ctx, info := tyr.WithRequestInfo(r.Context())
next.ServeHTTP(w, r.WithContext(ctx))
route := info.Route()
op, ok := info.Operation()
```

There is no logger in the context: code logs with `slog.InfoContext(ctx, ...)`, and `tyr.NewLogHandler` adds the request ID and the operation of the context to every record.

## Example

[`examples/shortlink`](examples/shortlink) is a small URL shortener built on tyr, the way a user would build it: an in-memory store, validation, `MapError`, authorization with an interceptor, middleware, graceful shutdown and end-to-end tests.

## Install

```sh
go get github.com/iaxel/tyr
```

tyr needs Go 1.27, for generic methods. With `GOTOOLCHAIN=auto`, the default, the go command downloads that toolchain itself. The module depends on the standard library only.

## Status

v0.1: REST. The API may change until v1. Next:

- v0.2: JSON-RPC 2.0, the same operations at `POST /rpc`
- v0.3: contracts (`Define`), a typed client, an in-process client for tests
- then: JSON Schema, OpenAPI 3.1 and OpenRPC from the same types; OpenTelemetry, timeouts, CORS and a go-playground adapter

## The name

Týr, the Norse god of law and oaths, put his hand in Fenrir's jaws as the pledge of a fair deal; tyr keeps contracts the compiler checks. ᛏ is his rune.

## License

[MIT](LICENSE)
