package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesFavicon(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/favicon.svg", nil)
	response := httptest.NewRecorder()

	Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if !strings.Contains(response.Body.String(), "<svg") {
		t.Fatalf("favicon response is not SVG: %q", response.Body.String())
	}
}

func TestIndexReferencesFavicon(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()

	Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if !strings.Contains(response.Body.String(), `href="/favicon.svg"`) {
		t.Fatal("index does not reference favicon")
	}
}
