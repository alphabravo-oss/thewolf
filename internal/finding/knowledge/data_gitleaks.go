package knowledge

// gitleaks default-ruleset secret-type mappings. Every gitleaks finding
// reduces to "hardcoded-secret" → "rotate-and-remove-secret"; the rule_id
// is the secret type (e.g. "aws-access-token"), and we register entries
// for the most common ones so the category is set even when the catch-all
// "generic-api-key" rule fires.

func init() {
	for _, e := range gitleaksEntries() {
		RegisterEntry(e)
	}
}

func gitleaksEntries() []Entry {
	const t = "gitleaks"
	const cat = "hardcoded-secret"
	const fix = "rotate-and-remove-secret"
	ids := []string{
		"generic-api-key",
		"aws-access-token", "aws-secret-key", "aws-session-token",
		"gcp-api-key", "gcp-service-account",
		"azure-storage-key", "azure-active-directory",
		"github-pat", "github-oauth", "github-fine-grained-pat", "github-app-token",
		"gitlab-pat", "gitlab-pipeline-trigger-token",
		"slack-bot-token", "slack-user-token", "slack-webhook-url",
		"stripe-access-token", "stripe-restricted-token",
		"private-key", "ssh-private-key", "pgp-private-key",
		"jwt", "jwt-base64",
		"npm-access-token",
		"twilio-api-key",
		"sendgrid-api-token",
		"openai-api-key",
		"anthropic-api-key",
		"databricks-api-token",
		"datadog-access-token",
		"discord-api-token", "discord-bot-token",
		"facebook-access-token", "facebook-secret",
		"google-oauth-client-secret", "google-api-key",
		"square-access-token", "square-secret",
		"twitter-access-token", "twitter-bearer-token",
		"linkedin-client-secret",
		"pypi-upload-token",
		"heroku-api-key",
		"mailgun-private-api-token",
		"hashicorp-vault-token",
	}
	out := make([]Entry, 0, len(ids))
	for _, id := range ids {
		out = append(out, Entry{
			Tool: t, RuleID: id, FineCategory: cat, FixStrategy: fix,
			References: []string{"CWE-798"},
		})
	}
	return out
}
