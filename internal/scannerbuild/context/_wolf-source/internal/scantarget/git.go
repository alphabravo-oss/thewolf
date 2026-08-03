package scantarget

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/secrets"
)

// CanonicalGitURL accepts only transport schemes that cannot invoke local Git
// helpers. It deliberately rejects file://, git://, ext::, local paths, and
// URLs containing inline credentials.
func CanonicalGitURL(raw string) (string, string, error) {
	value := strings.TrimSpace(raw)
	if strings.HasPrefix(value, "git@") && strings.Contains(value, ":") {
		parts := strings.SplitN(strings.TrimPrefix(value, "git@"), ":", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return "", "", fmt.Errorf("invalid Git SSH URL")
		}
		value = "ssh://git@" + parts[0] + "/" + strings.TrimPrefix(parts[1], "/")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" {
		return "", "", fmt.Errorf("Git URL must be an absolute HTTPS or SSH URL")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "ssh" {
		return "", "", fmt.Errorf("Git URL scheme %q is not allowed", parsed.Scheme)
	}
	if parsed.User != nil {
		if _, hasPassword := parsed.User.Password(); hasPassword {
			return "", "", fmt.Errorf("inline Git credentials are not allowed; use credential_id")
		}
		if parsed.Scheme == "https" && parsed.User.Username() != "" {
			return "", "", fmt.Errorf("inline Git credentials are not allowed; use credential_id")
		}
	}
	if parsed.Scheme == "ssh" {
		if parsed.User == nil || !validSSHGitUsername(parsed.User.Username()) {
			return "", "", fmt.Errorf("SSH Git URLs require a safe explicit username")
		}
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", fmt.Errorf("Git URL query strings and fragments are not allowed")
	}
	if parsed.Path == "" || parsed.Path == "/" {
		return "", "", fmt.Errorf("Git URL must include a repository path")
	}
	host := strings.ToLower(parsed.Hostname())
	if !validGitHostname(host) {
		return "", "", fmt.Errorf("Git URL contains an invalid hostname")
	}
	port := parsed.Port()
	if (parsed.Scheme == "https" && port == "443") || (parsed.Scheme == "ssh" && port == "22") {
		port = ""
	}
	if port != "" {
		parsed.Host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		parsed.Host = "[" + host + "]"
	} else {
		parsed.Host = host
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	return parsed.String(), host, nil
}

func validSSHGitUsername(username string) bool {
	if username == "" || len(username) > 64 || username[0] == '-' {
		return false
	}
	for _, character := range username {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '.' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func validGitHostname(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character >= 'a' && character <= 'z') ||
				(character >= '0' && character <= '9') || character == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func (r Resolver) prepareGit(ctx context.Context, repo *models.Repo, ref string) (Prepared, error) {
	canonical, host, err := CanonicalGitURL(repo.SourcePath)
	if err != nil {
		return Prepared{}, err
	}
	if err := ValidateRemoteDestination(ctx, host); err != nil {
		return Prepared{}, err
	}
	if ref == "" {
		ref = repo.DefaultBranch
	}
	dir, err := makeScanWorkspace("wolf-git-scan-*")
	if err != nil {
		return Prepared{}, fmt.Errorf("create Git workspace: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1")
	credentialCleanup := func() {}
	if repo.CredentialSecretID != "" {
		env, credentialCleanup, err = r.gitCredentialEnv(ctx, repo, host, canonical, dir, env)
		if err != nil {
			cleanup()
			return Prepared{}, err
		}
	}
	defer credentialCleanup()

	run := func(args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		cmd.Env = env
		out, runErr := cmd.CombinedOutput()
		if runErr != nil {
			return nil, fmt.Errorf("git command failed: %s", truncateGitOutput(string(out)))
		}
		return out, nil
	}
	if _, err := run("init", "--quiet"); err != nil {
		cleanup()
		return Prepared{}, err
	}
	if _, err := run("-c", "protocol.file.allow=never", "remote", "add", "origin", canonical); err != nil {
		cleanup()
		return Prepared{}, err
	}
	// Resolve again immediately before the network operation. This narrows
	// the validation/connect window and rejects destinations that changed
	// from an approved address to a blocked one during preparation.
	if err := ValidateRemoteDestination(ctx, host); err != nil {
		cleanup()
		return Prepared{}, err
	}
	fetchArgs := []string{"-c", "protocol.file.allow=never", "fetch", "--quiet", "--depth=1", "--no-tags", "origin"}
	if strings.TrimSpace(ref) != "" {
		fetchArgs = append(fetchArgs, strings.TrimSpace(ref))
	}
	if _, err := run(fetchArgs...); err != nil {
		cleanup()
		return Prepared{}, fmt.Errorf("fetch Git source %s: %w", host, err)
	}
	if _, err := run("checkout", "--quiet", "--detach", "FETCH_HEAD"); err != nil {
		cleanup()
		return Prepared{}, err
	}
	shaOutput, err := run("rev-parse", "HEAD")
	if err != nil {
		cleanup()
		return Prepared{}, err
	}
	sha := strings.TrimSpace(string(shaOutput))
	treeOutput, err := run("rev-parse", "HEAD^{tree}")
	if err != nil {
		cleanup()
		return Prepared{}, err
	}
	return Prepared{
		Path: dir, SourceType: models.SourceTypeGit, SourcePath: canonical,
		CommitSHA: sha, TreeDigest: "git:" + strings.TrimSpace(string(treeOutput)),
		DirtyState: "clean", PreparedWorkspace: dir, Cleanup: cleanup,
	}, nil
}

func (r Resolver) gitCredentialEnv(ctx context.Context, repo *models.Repo, host, canonical, dir string, env []string) ([]string, func(), error) {
	if r.Store == nil {
		return nil, nil, fmt.Errorf("credential store is unavailable")
	}
	credential, err := r.Store.GetSecretByID(ctx, repo.CredentialSecretID)
	if err != nil || credential.UserID != repo.UserID {
		return nil, nil, fmt.Errorf("Git credential not found")
	}
	if !credentialAllowsHost(credential, host) {
		return nil, nil, fmt.Errorf("Git credential is not allowed for host %s", host)
	}
	plaintext, err := secrets.Decrypt(credential.EncryptedValue)
	if err != nil {
		return nil, nil, fmt.Errorf("decrypt Git credential: %w", err)
	}
	parsed, _ := url.Parse(canonical)
	switch credential.KeyType {
	case models.KeyTypeGitHTTPS, models.KeyTypeGitHubToken, models.KeyTypeGitLabToken:
		if parsed.Scheme != "https" {
			return nil, nil, fmt.Errorf("credential type %s requires an HTTPS Git URL", credential.KeyType)
		}
		username := "git"
		var metadata map[string]interface{}
		_ = json.Unmarshal([]byte(credential.MetadataJSON), &metadata)
		if value, ok := metadata["username"].(string); ok && value != "" {
			username = value
		} else if credential.KeyType == models.KeyTypeGitHubToken {
			username = "x-access-token"
		} else if credential.KeyType == models.KeyTypeGitLabToken {
			username = "oauth2"
		}
		askpass := filepath.Join(dir, ".wolf-git-askpass")
		script := "#!/bin/sh\ncase \"$1\" in\n*Username*) printf '%s' \"$WOLF_GIT_USERNAME\" ;;\n*) printf '%s' \"$WOLF_GIT_PASSWORD\" ;;\nesac\n"
		if err := os.WriteFile(askpass, []byte(script), 0o700); err != nil {
			return nil, nil, fmt.Errorf("write Git credential helper: %w", err)
		}
		env = append(env,
			"GIT_ASKPASS="+askpass,
			"WOLF_GIT_USERNAME="+username,
			"WOLF_GIT_PASSWORD="+plaintext,
		)
		return env, func() { _ = os.Remove(askpass) }, nil
	case models.KeyTypeSSHPrivate:
		if parsed.Scheme != "ssh" {
			return nil, nil, fmt.Errorf("SSH private key requires an SSH Git URL")
		}
		var metadata map[string]interface{}
		_ = json.Unmarshal([]byte(credential.MetadataJSON), &metadata)
		knownHosts, _ := metadata["known_hosts"].(string)
		if strings.TrimSpace(knownHosts) == "" {
			return nil, nil, fmt.Errorf("SSH Git credential is missing known_hosts")
		}
		keyPath := filepath.Join(dir, ".wolf-git-key")
		hostsPath := filepath.Join(dir, ".wolf-known-hosts")
		if err := os.WriteFile(keyPath, []byte(plaintext), 0o600); err != nil {
			return nil, nil, err
		}
		if err := os.WriteFile(hostsPath, []byte(knownHosts+"\n"), 0o600); err != nil {
			_ = os.Remove(keyPath)
			return nil, nil, err
		}
		sshCommand := strings.Join([]string{
			"ssh", "-i", shellQuote(keyPath), "-o", "IdentitiesOnly=yes",
			"-o", "StrictHostKeyChecking=yes", "-o", "UserKnownHostsFile=" + shellQuote(hostsPath),
		}, " ")
		env = append(env, "GIT_SSH_COMMAND="+sshCommand)
		return env, func() {
			_ = os.Remove(keyPath)
			_ = os.Remove(hostsPath)
		}, nil
	default:
		return nil, nil, fmt.Errorf("credential type %s cannot be used for Git", credential.KeyType)
	}
}

func credentialAllowsHost(credential *models.Secret, host string) bool {
	var allowed []string
	if json.Unmarshal([]byte(credential.AllowedHosts), &allowed) != nil {
		return false
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, candidate := range allowed {
		candidate = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(candidate), "."))
		if candidate == host {
			return true
		}
		if strings.HasPrefix(candidate, "*.") && strings.HasSuffix(host, strings.TrimPrefix(candidate, "*")) {
			return true
		}
	}
	return false
}

// ValidateRemoteDestination applies the network boundary shared by API-managed
// Git and SSH sources. Private destinations require an explicit operator CIDR
// allowlist; special-purpose local destinations are always rejected.
func ValidateRemoteDestination(ctx context.Context, host string) error {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return fmt.Errorf("Git destination %s is not allowed", host)
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return fmt.Errorf("resolve Git destination %s: %w", host, err)
	}
	allowedPrivate := configuredPrivateCIDRs()
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
			ip.IsMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("Git destination %s resolves to a blocked address", host)
		}
		if ip.IsPrivate() && !ipAllowedByCIDRs(ip, allowedPrivate) {
			return fmt.Errorf("Git destination %s resolves to a private address not allowed by WOLF_GIT_ALLOWED_CIDRS", host)
		}
	}
	return nil
}

func configuredPrivateCIDRs() []*net.IPNet {
	var result []*net.IPNet
	for _, raw := range strings.Split(os.Getenv("WOLF_GIT_ALLOWED_CIDRS"), ",") {
		_, network, err := net.ParseCIDR(strings.TrimSpace(raw))
		if err == nil {
			result = append(result, network)
		}
	}
	return result
}

func ipAllowedByCIDRs(ip net.IP, allowed []*net.IPNet) bool {
	for _, network := range allowed {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func truncateGitOutput(output string) string {
	output = strings.TrimSpace(output)
	if len(output) > 500 {
		output = output[:500] + "…"
	}
	if output == "" {
		return "exit status unavailable"
	}
	return output
}
