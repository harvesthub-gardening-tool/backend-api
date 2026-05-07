package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func hasCSVToken(header, token string) bool {
	for part := range strings.SplitSeq(header, ",") {
		if strings.TrimSpace(part) == token {
			return true
		}
	}
	return false
}

func TestCORSOptionsRequest(t *testing.T) {
	nextCalled := false
	handler := cors(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		nextCalled = true
	}))

	req := httptest.NewRequest(http.MethodOptions, "/garden.v2.GardenService/GetSummary", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if nextCalled {
		t.Fatal("expected OPTIONS preflight to short-circuit")
	}
	if got := w.Code; got != http.StatusOK {
		t.Fatalf("status = %d, want %d", got, http.StatusOK)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want *", got)
	}
	methods := w.Header().Get("Access-Control-Allow-Methods")
	if !hasCSVToken(methods, http.MethodGet) || !hasCSVToken(methods, http.MethodPost) || !hasCSVToken(methods, http.MethodOptions) {
		t.Fatalf("Access-Control-Allow-Methods missing expected tokens: %q", methods)
	}
	if got := w.Header().Get("Access-Control-Allow-Headers"); got == "" {
		t.Fatal("expected Access-Control-Allow-Headers to be set")
	}
}

func TestCORSPassesThroughNonOptions(t *testing.T) {
	nextCalled := false
	handler := cors(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusCreated)
	}))

	req := httptest.NewRequest(http.MethodPost, "/garden.v2.GardenService/GetSummary", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if !nextCalled {
		t.Fatal("expected non-OPTIONS requests to reach next handler")
	}
	if got := w.Code; got != http.StatusCreated {
		t.Fatalf("status = %d, want %d", got, http.StatusCreated)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want *", got)
	}
}

func TestParseEnvInt(t *testing.T) {
	t.Run("returns fallback when variable is missing", func(t *testing.T) {
		const key = "TEST_PARSE_ENV_INT_MISSING"
		t.Setenv(key, "")

		if got := parseEnvInt(key, 7); got != 7 {
			t.Fatalf("parseEnvInt() = %d, want 7", got)
		}
	})

	t.Run("parses integer values", func(t *testing.T) {
		const key = "TEST_PARSE_ENV_INT_VALID"
		t.Setenv(key, "42")

		if got := parseEnvInt(key, 7); got != 42 {
			t.Fatalf("parseEnvInt() = %d, want 42", got)
		}
	})

	t.Run("returns fallback when value is invalid", func(t *testing.T) {
		const key = "TEST_PARSE_ENV_INT_INVALID"
		t.Setenv(key, "not-a-number")

		if got := parseEnvInt(key, 7); got != 7 {
			t.Fatalf("parseEnvInt() = %d, want 7", got)
		}
	})
}

func TestParseEnvIntUsesProcessEnv(t *testing.T) {
	const key = "TEST_PARSE_ENV_INT_PROCESS"
	original, hadOriginal := os.LookupEnv(key)
	t.Cleanup(func() {
		if hadOriginal {
			_ = os.Setenv(key, original)
			return
		}
		_ = os.Unsetenv(key)
	})

	if err := os.Setenv(key, "9"); err != nil {
		t.Fatalf("Setenv: %v", err)
	}

	if got := parseEnvInt(key, 1); got != 9 {
		t.Fatalf("parseEnvInt() = %d, want 9", got)
	}
}
