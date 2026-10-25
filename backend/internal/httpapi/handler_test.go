package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeJSONRequiresObject(t *testing.T) {
	for _, body := range []string{"null", " \nnull\t", "[]", `"text"`, "42", "true", "", `{"name":"ok"} {}`, `{"unknown":true}`} {
		t.Run(body, func(t *testing.T) {
			request := httptest.NewRequest("PUT", "/", strings.NewReader(body))
			response := httptest.NewRecorder()
			var target struct {
				Name string `json:"name"`
			}
			if decodeJSON(response, request, &target) || response.Code != 400 {
				t.Fatalf("accepted invalid request %q: status %d", body, response.Code)
			}
		})
	}
}

func TestDecodeJSONAcceptsObject(t *testing.T) {
	request := httptest.NewRequest("PUT", "/", strings.NewReader(" {\"name\":\"ok\"} \n"))
	response := httptest.NewRecorder()
	var target struct {
		Name string `json:"name"`
	}
	if !decodeJSON(response, request, &target) || target.Name != "ok" {
		t.Fatalf("valid object rejected: %s", response.Body.String())
	}
}
