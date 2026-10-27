package secrets

import "testing"

func TestSensitive(t *testing.T) {
	for _, p := range []string{".env", "web/.env.local", ".envrc", "certs/server.pem", ".git/config", "home/.ssh/id_rsa", "config/credentials.json", "id_ed25519", ".git-credentials", "infra/prod.tfvars", "gcp/service-account-prod.json", ".aws/config",
		// Claude's own login and settings, and other tools' tokens
		".credentials.json", ".claude/.credentials.json", ".claude.json", ".claude/settings.local.json",
		".kube/config", ".docker/config.json", ".config/gh/hosts.yml", ".config/gcloud/application_default_credentials.json",
		".gnupg/private-keys-v1.d/x.key", ".azure/msal_token_cache.json", ".zsh_history", ".bash_history", ".python_history",
		".terraform.d/credentials.tfrc.json"} {
		if !Sensitive(p) {
			t.Errorf("%s should be sensitive", p)
		}
	}
	for _, p := range []string{"env.go", "src/environment.ts", "docs/keys.md", "secretary.go", "keyboard.tsx",
		"history.go", "src/hosts.go", "docs/credentials-guide.md", "k8s/deploy.yaml", ".claude/agents/reviewer.md", "docker/config.go"} {
		if Sensitive(p) {
			t.Errorf("%s shouldn't be sensitive", p)
		}
	}
}
