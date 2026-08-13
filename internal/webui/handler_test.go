package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesIndex(t *testing.T) {
	handler := Handler()
	for _, path := range []string{"/", "/overview"} {
		request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1"+path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("path=%s status=%d", path, response.Code)
		}
		if !strings.Contains(response.Body.String(), "buff-go") {
			t.Fatalf("path=%s body=%q", path, response.Body.String())
		}
	}
}
