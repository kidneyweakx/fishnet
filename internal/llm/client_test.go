package llm

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckProxy_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	// CheckProxy expects baseURL without trailing slash; strip the httptest URL path.
	if !CheckProxy(srv.URL) {
		t.Fatal("expected CheckProxy to return true for a healthy server")
	}
}

func TestCheckProxy_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if CheckProxy(srv.URL) {
		t.Fatal("expected CheckProxy to return false when /healthz returns 404")
	}
}

func TestCheckProxy_Unreachable(t *testing.T) {
	// Use a port that is not listening.
	if CheckProxy("http://127.0.0.1:19999") {
		t.Fatal("expected CheckProxy to return false for unreachable server")
	}
}

func TestCheckProxy_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	if CheckProxy(srv.URL) {
		t.Fatal("expected CheckProxy to return false when server returns 500")
	}
}
