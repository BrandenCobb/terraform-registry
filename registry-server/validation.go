package main

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/gorilla/mux"
)

var (
	artifactNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	hostnameRE     = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	providerFileRE = regexp.MustCompile(`^terraform-provider-([A-Za-z0-9][A-Za-z0-9_-]{0,63})_([0-9A-Za-z.+-]+)_([A-Za-z0-9][A-Za-z0-9_-]{0,63})_([A-Za-z0-9][A-Za-z0-9_-]{0,63})(?:_[a-f0-9]{64})?\.zip$`)
	checksumFileRE = regexp.MustCompile(`^terraform-provider-([A-Za-z0-9][A-Za-z0-9_-]{0,63})_([0-9A-Za-z.+-]+)_SHA256SUMS(?:\.sig)?$`)
	moduleFileRE   = regexp.MustCompile(`^module(?:_[a-f0-9]{64})?\.tar\.gz$`)
)

func validArtifactName(v string) bool { return artifactNameRE.MatchString(v) }
func validVersion(v string) bool      { _, ok := parseSemanticVersion(v); return ok }
func validHostname(v string) bool     { return hostnameRE.MatchString(v) && !strings.Contains(v, "..") }

func isPublicArtifactPath(path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 5 && parts[0] == "providers" {
		if !validArtifactName(parts[1]) || !validArtifactName(parts[2]) || !validVersion(parts[3]) {
			return false
		}
		match := providerFileRE.FindStringSubmatch(parts[4])
		if len(match) == 5 {
			return match[1] == parts[2] && match[2] == parts[3] && validArtifactName(match[3]) && validArtifactName(match[4])
		}
		checksumMatch := checksumFileRE.FindStringSubmatch(parts[4])
		return len(checksumMatch) == 3 && checksumMatch[1] == parts[2] && checksumMatch[2] == parts[3]
	}
	if len(parts) == 6 && parts[0] == "modules" && moduleFileRE.MatchString(parts[5]) {
		return validArtifactName(parts[1]) && validArtifactName(parts[2]) && validArtifactName(parts[3]) && validVersion(parts[4])
	}
	return false
}

func routeValidationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for key, value := range mux.Vars(r) {
			valid := true
			switch key {
			case "namespace", "name", "type", "provider", "os", "arch":
				valid = validArtifactName(value)
			case "version":
				valid = validVersion(value)
			case "hostname":
				valid = validHostname(value)
			}
			if !valid {
				httpJSONError(w, http.StatusBadRequest, "Invalid "+key)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; img-src 'self' data:; object-src 'none'; frame-ancestors 'none'; base-uri 'none'")
		next.ServeHTTP(w, r)
	})
}
