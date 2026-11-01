package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pulse-reader/app/internal/store"
)

func TestWriteStoreErrorMapsKnownErrors(t *testing.T) {
	notFound := apiError{http.StatusNotFound, "thing.not_found", "The thing was not found."}
	taken := apiError{http.StatusConflict, "thing.taken", "The thing already exists."}
	cases := []storeError{{store.ErrNotFound, notFound}, {store.ErrDuplicate, taken}}
	fallback := apiError{http.StatusInternalServerError, "thing.failed", "The thing failed."}
	tests := []struct {
		name string
		err  error
		want apiError
	}{
		{"direct", store.ErrNotFound, notFound},
		{"wrapped", fmt.Errorf("context: %w", store.ErrDuplicate), taken},
		{"unknown", errors.New("connection reset"), fallback},
		{"first match wins", errors.Join(store.ErrDuplicate, store.ErrNotFound), notFound},
		{"no cases", store.ErrNotFound, fallback},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			mapping := cases
			if test.name == "no cases" {
				mapping = nil
			}
			writeStoreError(response, test.err, mapping, fallback)
			var body map[string]string
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if response.Code != test.want.status || body["code"] != test.want.code || body["error"] != test.want.message {
				t.Fatalf("got %d %v, want %+v", response.Code, body, test.want)
			}
		})
	}
}
