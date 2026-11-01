package httpapi_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/pulse-reader/app/internal/httpapi"
)

// openAPIPath is the API description, relative to this test's directory.
const openAPIPath = "../../../openapi/openapi.json"

type openAPIOperation struct {
	Security  []map[string][]string `json:"security"`
	Responses map[string]struct {
		ErrorCodes []string `json:"x-error-codes"`
	} `json:"responses"`
}

func loadOpenAPI(t *testing.T) map[string]map[string]openAPIOperation {
	t.Helper()
	raw, err := os.ReadFile(openAPIPath)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		OpenAPI string                                 `json:"openapi"`
		Paths   map[string]map[string]openAPIOperation `json:"paths"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("%s is not valid JSON: %v", openAPIPath, err)
	}
	if !strings.HasPrefix(document.OpenAPI, "3.1") {
		t.Fatalf("openapi = %q, want 3.1.x", document.OpenAPI)
	}
	return document.Paths
}

func registeredRoutes(t *testing.T) []string {
	t.Helper()
	routes, ok := httpapi.New(nil, nil, false).(chi.Routes)
	if !ok {
		t.Fatal("httpapi.New no longer returns a chi router; update this test")
	}
	var registered []string
	err := chi.Walk(routes, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		registered = append(registered, method+" "+route)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return registered
}

func TestOpenAPIDescribesExactlyTheRegisteredRoutes(t *testing.T) {
	documented := map[string]bool{}
	for path, operations := range loadOpenAPI(t) {
		for method := range operations {
			documented[strings.ToUpper(method)+" "+path] = true
		}
	}
	registered := map[string]bool{}
	for _, route := range registeredRoutes(t) {
		registered[route] = true
	}
	var missing, extra []string
	for route := range registered {
		if !documented[route] {
			missing = append(missing, route)
		}
	}
	for route := range documented {
		if !registered[route] {
			extra = append(extra, route)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 || len(extra) > 0 {
		t.Fatalf("%s is out of date.\nRegistered but not documented: %v\nDocumented but not registered: %v", openAPIPath, missing, extra)
	}
}

func TestOpenAPIDocumentsSecurityAndResponses(t *testing.T) {
	public := []string{"GET /api/health", "GET /api/health/live", "GET /api/health/ready", "POST /api/auth/login", "POST /api/auth/logout"}
	for path, operations := range loadOpenAPI(t) {
		for method, operation := range operations {
			route := strings.ToUpper(method) + " " + path
			if len(operation.Responses) == 0 {
				t.Errorf("%s documents no responses", route)
			}
			isPublic := slices.Contains(public, route)
			if operation.Security == nil {
				t.Errorf("%s does not state its security (use [] for public routes)", route)
			} else if isPublic != (len(operation.Security) == 0) {
				t.Errorf("%s security = %v, public = %v", route, operation.Security, isPublic)
			}
		}
	}
}

// errorCodeLiteral matches the code argument of writeError(w, status, "code", ...)
// and of apiError{status, "code", ...} in the handler sources.
var errorCodeLiteral = regexp.MustCompile(`(?:writeError\(w, [^,]+|apiError\{[^,]+), "([a-z_]+\.[a-z_.]+)"`)

func TestOpenAPIListsEveryHandlerErrorCode(t *testing.T) {
	documented := map[string]bool{}
	for _, operations := range loadOpenAPI(t) {
		for _, operation := range operations {
			for _, response := range operation.Responses {
				for _, code := range response.ErrorCodes {
					documented[code] = true
				}
			}
		}
	}
	sources, err := filepath.Glob("../../../internal/httpapi/*.go")
	if err != nil || len(sources) == 0 {
		t.Fatalf("handler sources not found: %v", err)
	}
	used := map[string]bool{}
	for _, source := range sources {
		if strings.HasSuffix(source, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range errorCodeLiteral.FindAllStringSubmatch(string(raw), -1) {
			used[match[1]] = true
		}
	}
	for code := range used {
		if !documented[code] {
			t.Errorf("error code %q is returned by a handler but not listed in %s", code, openAPIPath)
		}
	}
	for code := range documented {
		if !used[code] {
			t.Errorf("error code %q is listed in %s but no handler returns it", code, openAPIPath)
		}
	}
}
