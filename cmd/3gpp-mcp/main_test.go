package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestBearerAuthMiddleware(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := bearerAuthMiddleware("secret-token", inner)

	t.Run("valid token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Bearer secret-token")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", w.Code)
		}
	})

	t.Run("wrong token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Bearer wrong-token")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})

	t.Run("missing header", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})

	t.Run("wrong scheme", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Basic secret-token")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})
}

// captureStdout runs fn and returns whatever it wrote to os.Stdout.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	done := make(chan string)
	go func() {
		data, _ := io.ReadAll(r)
		done <- string(data)
	}()

	fn()
	w.Close()
	return <-done
}

func TestCmdCompletion(t *testing.T) {
	tests := []struct {
		shell    string
		contains []string
	}{
		{
			shell:    "bash",
			contains: []string{"_3gpp_mcp", "complete -F _3gpp_mcp", "import-dir"},
		},
		{
			shell:    "zsh",
			contains: []string{"#compdef 3gpp-mcp", "_describe", "completion"},
		},
		{
			shell:    "fish",
			contains: []string{"complete -c 3gpp-mcp", "Start the MCP server"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.shell, func(t *testing.T) {
			out := captureStdout(t, func() {
				cmdCompletion([]string{tt.shell})
			})
			if out == "" {
				t.Errorf("expected non-empty output for %s", tt.shell)
			}
			for _, want := range tt.contains {
				if !strings.Contains(out, want) {
					t.Errorf("expected output to contain %q, got:\n%s", want, out)
				}
			}
		})
	}
}
