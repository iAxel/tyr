package links

import (
	"errors"
	"testing"

	"github.com/iaxel/tyr/examples/shortlink/store"
)

func TestCreateTakenRandomCode(t *testing.T) {
	s := New(store.New())
	tries := 0
	s.newCode = func() string {
		tries++
		return "same"
	}
	ctx := t.Context()
	if _, err := s.Create(ctx, CreateReq{URL: "https://go.dev"}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// Every random code is taken: the client didn't choose it, so the
	// error isn't the conflict of ErrExists, which MapError would report.
	tries = 0
	_, err := s.Create(ctx, CreateReq{URL: "https://go.dev"})
	if err == nil || errors.Is(err, store.ErrExists) || tries != 3 {
		t.Errorf("Create() error = %v after %d tries, want an error other than ErrExists after 3", err, tries)
	}
}
