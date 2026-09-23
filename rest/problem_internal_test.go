package rest

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteErrorOfOtherType(t *testing.T) {
	// Operation.Call returns only *tyr.Error; another error would reach
	// writeError as nil and still be sent, as an internal one.
	rec := httptest.NewRecorder()
	writeError(t.Context(), slog.New(slog.DiscardHandler), rec, nil, nil)

	want := `{"type":"about:blank","title":"Internal Server Error","status":500,"detail":"internal error","kind":"internal"}`
	if rec.Code != http.StatusInternalServerError || rec.Body.String() != want {
		t.Errorf("writeError() = %d %s, want 500 %s", rec.Code, rec.Body, want)
	}
}
