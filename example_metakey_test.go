package tyr_test

import (
	"context"
	"fmt"
	"slices"

	"github.com/iaxel/tyr"
	"github.com/iaxel/tyr/ctxkey"
)

// In an application, roles, requireRoles and authorize would live in an
// authz package, as authz.Require and authz.Interceptor.

var roles = tyr.NewMetaKey[[]string]("authz.roles")

// userRole carries the caller's role, as authentication middleware would
// set it.
var userRole = ctxkey.New[string]("user.role")

// requireRoles allows an operation only to callers with one of the roles.
func requireRoles(r ...string) tyr.OpOption {
	return roles.Option(r)
}

// authorize enforces the roles that operations require.
func authorize(ctx context.Context, op *tyr.Operation, req any, next tyr.Invoker) (any, error) {
	if want, ok := roles.From(op); ok {
		if role, _ := userRole.Get(ctx); !slices.Contains(want, role) {
			return nil, tyr.PermissionDenied("requires one of %v", want)
		}
	}
	return next(ctx, req)
}

func ExampleMetaKey() {
	api := tyr.New()
	api.Use(authorize)
	admin := api.Group(requireRoles("admin"))
	purge := admin.Handle("links.purge", func(ctx context.Context, req struct{}) (string, error) {
		return "purged", nil
	})

	for _, role := range []string{"admin", "guest"} {
		res, err := purge.Call(userRole.Set(context.Background(), role), nil)
		fmt.Println(role, res, err)
	}
	// Output:
	// admin purged <nil>
	// guest <nil> permission_denied: requires one of [admin]
}
