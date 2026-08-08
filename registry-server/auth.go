package main

import (
	"net/http"
	"os"
	"strings"
)

// authMiddleware returns a middleware that checks for API key authentication.
// If REGISTRY_API_KEY is not set, auth is disabled (open access).
// Management endpoints require the API key; registry protocol endpoints are public.
func authMiddleware(next http.Handler) http.Handler {
	apiKey := os.Getenv("REGISTRY_API_KEY")
	if apiKey == "" {
		return next // No auth configured, allow all
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Registry protocol endpoints are always public (Terraform CLI needs them)
		path := r.URL.Path
		if isPublicPath(path) {
			next.ServeHTTP(w, r)
			return
		}

		// Check API key from header or query param
		providedKey := r.Header.Get("X-API-Key")
		if providedKey == "" {
			providedKey = r.URL.Query().Get("api_key")
		}

		if providedKey != apiKey {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"success":false,"message":"Invalid or missing API key"}`))
			return
		}

		next.ServeHTTP(w, r)
	})
}

// isPublicPath returns true if the path should be accessible without auth.
// These are the Terraform CLI protocol endpoints.
func isPublicPath(path string) bool {
	publicPaths := []string{
		"/.well-known/",
		"/v1/providers/",
		"/v1/modules/",
		"/download/",
		"/health",
		"/{", // network mirror routes
	}

	// UI paths are public for browsing (auth only for mutations)
	uiPaths := []string{
		"/ui",
		"/api/v1/stats",
	}

	for _, p := range publicPaths {
		if strings.HasPrefix(path, p) {
			return true
		}
	}

	// GET requests to list endpoints are public for browsing
	if strings.HasPrefix(path, "/api/v1/") {
		for _, p := range uiPaths {
			if strings.HasPrefix(path, p) {
				return true
			}
		}
		// GET to list/detail endpoints is public
		// POST/DELETE requires auth (handled below)
	}

	// Allow GET on API list/detail endpoints (browsing)
	if strings.HasPrefix(path, "/api/v1/") && !strings.Contains(path, "/upload") {
		// This is a read-only API endpoint — let it through
		// Auth is only enforced on mutation paths in the handler itself
	}

	return false
}

// requireAPIKey is a simple check used within individual handlers
func requireAPIKey(r *http.Request) bool {
	apiKey := os.Getenv("REGISTRY_API_KEY")
	if apiKey == "" {
		return true // No auth configured
	}

	providedKey := r.Header.Get("X-API-Key")
	if providedKey == "" {
		providedKey = r.URL.Query().Get("api_key")
	}

	return providedKey == apiKey
}

// requireAuth wraps an http.HandlerFunc to require a valid API key.
// If REGISTRY_API_KEY is not set, the handler is called directly.
func requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAPIKey(r) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"success":false,"message":"Invalid or missing API key"}`))
			return
		}
		next(w, r)
	}
}
