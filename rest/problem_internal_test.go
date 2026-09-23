package rest

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/iaxel/tyr"
)

func TestWriteErrorOfOtherType(t *testing.T) {
	// Operation.Call returns only *tyr.Error, but another error would still
	// be sent, as an internal one.
	rec := httptest.NewRecorder()
	h := &handler{api: tyr.New()}
	h.writeError(t.Context(), rec, errors.New("odd"))

	want := `{"type":"about:blank","title":"Internal Server Error","status":500,"detail":"internal error","kind":"internal"}`
	if rec.Code != http.StatusInternalServerError || rec.Body.String() != want {
		t.Errorf("writeError() = %d %s, want 500 %s", rec.Code, rec.Body, want)
	}
}
