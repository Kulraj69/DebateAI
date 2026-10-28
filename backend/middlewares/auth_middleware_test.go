package middlewares

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAuthMiddlewareRejectsInvalidRequests(t *testing.T) {
	// This fixture has no credentials. Tests never connect to the database.
	configPath := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(configPath, []byte("{}\n"), 0600); err != nil {
		t.Fatal("could not create empty test configuration")
	}
	previousMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(previousMode) })
	var output bytes.Buffer
	previousWriter := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previousWriter) })

	for _, tc := range []struct {
		name   string
		header string
		error  string
	}{
		{"missing", "", "Authorization header is required"},
		{"wrong scheme", "Basic malformed", "Invalid token format"},
		{"malformed", "Bearer malformed", "Invalid or expired token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			called := false
			router.GET("/protected", AuthMiddleware(configPath), func(c *gin.Context) {
				called = true
				c.Status(http.StatusOK)
			})
			request := httptest.NewRequest(http.MethodGet, "/protected", nil)
			request.Header.Set("Authorization", tc.header)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized || called {
				t.Fatal("invalid authentication must abort with Unauthorized")
			}
			var body map[string]string
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body["error"] != tc.error {
				t.Fatal("unexpected authentication error response")
			}
		})
	}
	if !strings.Contains(output.String(), "JWT validation failed for path /protected") {
		t.Fatal("failed validation should retain diagnostic logging")
	}
	for _, removed := range []string{"Current server time", "Token expiration time", "Time until expiration"} {
		if strings.Contains(output.String(), removed) {
			t.Fatalf("failed validation should not log %q", removed)
		}
	}
}
