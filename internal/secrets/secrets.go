// Package secrets recognizes files that usually hold credentials, so htd
// never sends them to a model, even a local one.
package secrets

import (
	"path/filepath"
	"regexp"
)

var names = regexp.MustCompile(`(?i)` +
	// files by name
	`(^|/)(\.env(\..*)?|\.envrc|\.npmrc|\.netrc|\.pgpass|\.git-credentials|\.pypirc|id_(rsa|dsa|ecdsa|ed25519)(\.pub)?|\.?credentials(\.json|\.tfrc\.json)?|secrets?\.(json|ya?ml|toml)|service-account.*\.json|\.claude\.json|settings\.local\.json|\.[a-z_]*_history|hosts\.ya?ml)$` +
	// files by extension
	`|\.(pem|key|p12|pfx|keystore|jks|tfvars|tfstate)$` +
	// folders that hold credentials or config with tokens
	`|(^|/)\.(git|ssh|aws|gnupg|kube|azure)/|(^|/)\.docker/config\.json$|(^|/)\.config/gcloud/`)

// Sensitive reports whether a repo-relative path looks like it holds secrets.
func Sensitive(rel string) bool {
	return names.MatchString(filepath.ToSlash(rel))
}
