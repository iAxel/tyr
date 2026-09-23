package rest

import (
	"bytes"
	"encoding/json/v2"
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

func TestMinimalProblem(t *testing.T) {
	// Written without the encoder, it's what the encoder writes.
	for _, status := range []int{400, 404, 413, 499, 500, 503, 599} {
		want, err := json.Marshal(problem{Type: "about:blank", Title: statusText(status), Status: status})
		if err != nil {
			t.Fatal(err)
		}
		if got := minimalProblem(status); !bytes.Equal(got, want) {
			t.Errorf("minimalProblem(%d) = %s, want %s", status, got, want)
		}
	}
}
