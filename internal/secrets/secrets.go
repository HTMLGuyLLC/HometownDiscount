// Package secrets recognizes files that usually hold credentials, so htd
// never sends them to a model, even a local one.
package secrets

import (
	"path/filepath"
	"regexp"
)

var names = regexp.MustCompile(`(?i)(^|/)(\.env(\..*)?|\.envrc|\.npmrc|\.netrc|\.pgpass|\.git-credentials|\.pypirc|id_(rsa|dsa|ecdsa|ed25519)(\.pub)?|credentials(\.json)?|secrets?\.(json|ya?ml|toml)|service-account.*\.json)$|\.(pem|key|p12|pfx|keystore|jks|tfvars|tfstate)$|(^|/)\.git/|(^|/)\.ssh/|(^|/)\.aws/`)

// Sensitive reports whether a repo-relative path looks like it holds secrets.
func Sensitive(rel string) bool {
	return names.MatchString(filepath.ToSlash(rel))
}
