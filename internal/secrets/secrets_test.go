package secrets

import "testing"

func TestSensitive(t *testing.T) {
	for _, p := range []string{".env", "web/.env.local", ".envrc", "certs/server.pem", ".git/config", "home/.ssh/id_rsa", "config/credentials.json", "id_ed25519", ".git-credentials", "infra/prod.tfvars", "gcp/service-account-prod.json", ".aws/config"} {
		if !Sensitive(p) {
			t.Errorf("%s should be sensitive", p)
		}
	}
	for _, p := range []string{"env.go", "src/environment.ts", "docs/keys.md", "secretary.go", "keyboard.tsx"} {
		if Sensitive(p) {
			t.Errorf("%s shouldn't be sensitive", p)
		}
	}
}
