package latest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/scannertools/manifest"
)

type Checker struct {
	Client *http.Client
	Now    func() time.Time
}

func (c Checker) Check(ctx context.Context, name string, tool manifest.Tool) models.ScannerVersionCheck {
	now := time.Now().UTC()
	if c.Now != nil {
		now = c.Now().UTC()
	}
	out := models.ScannerVersionCheck{
		ToolName:      name,
		PinnedVersion: tool.PinnedVersion,
		Status:        models.ScannerVersionUnknown,
		CheckedAt:     now,
		SourceType:    tool.UpdateSource.Type,
		SourceURL:     sourceURL(tool.UpdateSource),
	}
	if tool.PinnedVersion == "" {
		out.Error = "tool has no pinned version"
		return out
	}

	latestVersion, latestRef, err := c.latest(ctx, tool.UpdateSource)
	if err != nil {
		out.Status = models.ScannerVersionCheckFailed
		out.Error = err.Error()
		return out
	}
	if latestVersion == "" {
		out.Status = models.ScannerVersionUnknown
		return out
	}
	out.LatestVersion = latestVersion
	out.LatestReference = latestRef
	if CompareVersions(latestVersion, tool.PinnedVersion) > 0 {
		out.Status = models.ScannerVersionUpdateAvailable
	} else {
		out.Status = models.ScannerVersionCurrent
	}
	return out
}

func (c Checker) latest(ctx context.Context, source manifest.UpdateSource) (version, reference string, err error) {
	switch source.Type {
	case "pypi":
		return c.latestPyPI(ctx, source.Package)
	case "npm":
		return c.latestNPM(ctx, source.Package)
	case "github_releases":
		return c.latestGitHubRelease(ctx, source.Owner, source.Repo, source.TagPattern)
	case "docker_registry":
		return c.latestDockerTag(ctx, source.Repository, source.TagPattern)
	case "rubygems":
		return c.latestRubyGems(ctx, source.Package)
	case "go_module":
		return c.latestGoModule(ctx, source.Module)
	case "packagist":
		return c.latestPackagist(ctx, source.Package)
	default:
		return "", "", nil
	}
}

func (c Checker) httpClient() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return &http.Client{Timeout: 10 * time.Second}
}

func (c Checker) getJSON(ctx context.Context, url string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(dst)
}

func (c Checker) latestPyPI(ctx context.Context, pkg string) (string, string, error) {
	if pkg == "" {
		return "", "", fmt.Errorf("pypi package is empty")
	}
	var resp struct {
		Info struct {
			Version string `json:"version"`
		} `json:"info"`
	}
	u := "https://pypi.org/pypi/" + url.PathEscape(pkg) + "/json"
	if err := c.getJSON(ctx, u, &resp); err != nil {
		return "", "", err
	}
	return resp.Info.Version, "pypi:" + pkg + "@" + resp.Info.Version, nil
}

func (c Checker) latestNPM(ctx context.Context, pkg string) (string, string, error) {
	if pkg == "" {
		return "", "", fmt.Errorf("npm package is empty")
	}
	var resp struct {
		DistTags map[string]string `json:"dist-tags"`
	}
	u := "https://registry.npmjs.org/" + strings.ReplaceAll(pkg, "/", "%2f")
	if err := c.getJSON(ctx, u, &resp); err != nil {
		return "", "", err
	}
	v := resp.DistTags["latest"]
	return v, "npm:" + pkg + "@" + v, nil
}

func (c Checker) latestGitHubRelease(ctx context.Context, owner, repo, pattern string) (string, string, error) {
	if owner == "" || repo == "" {
		return "", "", fmt.Errorf("github owner/repo is empty")
	}
	var resp struct {
		TagName string `json:"tag_name"`
	}
	u := "https://api.github.com/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/releases/latest"
	if err := c.getJSON(ctx, u, &resp); err != nil {
		return c.latestGitHubTag(ctx, owner, repo, pattern)
	}
	if pattern != "" {
		if ok, _ := regexp.MatchString(pattern, resp.TagName); !ok {
			return "", "", fmt.Errorf("latest github tag %q does not match %s", resp.TagName, pattern)
		}
	}
	return NormalizeVersion(resp.TagName), "github:" + owner + "/" + repo + "@" + resp.TagName, nil
}

func (c Checker) latestGitHubTag(ctx context.Context, owner, repo, pattern string) (string, string, error) {
	var resp []struct {
		Name string `json:"name"`
	}
	u := "https://api.github.com/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/tags?per_page=100"
	if err := c.getJSON(ctx, u, &resp); err != nil {
		return "", "", err
	}
	tags := make([]string, 0, len(resp))
	for _, tag := range resp {
		tags = append(tags, tag.Name)
	}
	best := newestTag(tags, pattern)
	if best == "" {
		return "", "", nil
	}
	return NormalizeVersion(best), "github:" + owner + "/" + repo + "@" + best, nil
}

func (c Checker) latestRubyGems(ctx context.Context, pkg string) (string, string, error) {
	if pkg == "" {
		return "", "", fmt.Errorf("rubygems package is empty")
	}
	var resp struct {
		Version string `json:"version"`
	}
	u := "https://rubygems.org/api/v1/gems/" + url.PathEscape(pkg) + ".json"
	if err := c.getJSON(ctx, u, &resp); err != nil {
		return "", "", err
	}
	return resp.Version, "rubygems:" + pkg + "@" + resp.Version, nil
}

func (c Checker) latestGoModule(ctx context.Context, module string) (string, string, error) {
	if module == "" {
		return "", "", fmt.Errorf("go module is empty")
	}
	escaped := escapeGoModulePath(module)
	u := "https://proxy.golang.org/" + escaped + "/@v/list"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("GET %s: %s", u, resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}
	var tags []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			tags = append(tags, line)
		}
	}
	best := newestTag(tags, "")
	return NormalizeVersion(best), "go:" + module + "@" + best, nil
}

func (c Checker) latestPackagist(ctx context.Context, pkg string) (string, string, error) {
	if pkg == "" {
		return "", "", fmt.Errorf("packagist package is empty")
	}
	var resp struct {
		Package struct {
			Versions map[string]json.RawMessage `json:"versions"`
		} `json:"package"`
	}
	u := "https://repo.packagist.org/p2/" + strings.TrimPrefix(pkg, "/") + ".json"
	if err := c.getJSON(ctx, u, &resp); err != nil {
		return "", "", err
	}
	tags := make([]string, 0, len(resp.Package.Versions))
	for tag := range resp.Package.Versions {
		tags = append(tags, tag)
	}
	best := newestTag(tags, `^v?\d+\.\d+\.\d+`)
	return NormalizeVersion(best), "packagist:" + pkg + "@" + best, nil
}

func (c Checker) latestDockerTag(ctx context.Context, repo, pattern string) (string, string, error) {
	if repo == "" {
		return "", "", fmt.Errorf("docker repository is empty")
	}
	registry, path := splitDockerRepository(repo)
	tagsURL := "https://" + registry + "/v2/" + path + "/tags/list?n=100"
	tags, err := c.getDockerTags(ctx, tagsURL, registry, path)
	if err != nil {
		return "", "", err
	}
	best := newestTag(tags, pattern)
	if best == "" {
		return "", "", nil
	}
	return NormalizeVersion(best), repo + ":" + best, nil
}

func (c Checker) getDockerTags(ctx context.Context, tagsURL, registry, path string) ([]string, error) {
	var all []string
	var token string
	for nextURL := tagsURL; nextURL != ""; {
		tags, next, nextToken, err := c.getDockerTagsPage(ctx, nextURL, registry, path, token)
		if err != nil {
			return nil, err
		}
		if nextToken != "" {
			token = nextToken
		}
		all = append(all, tags...)
		nextURL = next
	}
	return all, nil
}

func (c Checker) getDockerTagsPage(ctx context.Context, tagsURL, registry, path, token string) (tags []string, nextURL string, nextToken string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tagsURL, nil)
	if err != nil {
		return nil, "", "", err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized && token == "" {
		challenge := resp.Header.Get("WWW-Authenticate")
		token, terr := c.dockerBearerToken(ctx, challenge, path)
		if terr != nil {
			return nil, "", "", terr
		}
		tags, nextURL, _, err := c.getDockerTagsPage(ctx, tagsURL, registry, path, token)
		return tags, nextURL, token, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", "", fmt.Errorf("GET docker tags %s/%s: %s", registry, path, resp.Status)
	}
	var payload struct {
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, "", "", err
	}
	return payload.Tags, dockerNextURL(tagsURL, resp.Header.Get("Link")), token, nil
}

func dockerNextURL(currentURL, linkHeader string) string {
	for _, part := range strings.Split(linkHeader, ",") {
		part = strings.TrimSpace(part)
		if !strings.Contains(part, `rel="next"`) {
			continue
		}
		start := strings.Index(part, "<")
		end := strings.Index(part, ">")
		if start < 0 || end <= start {
			continue
		}
		raw := part[start+1 : end]
		base, err := url.Parse(currentURL)
		if err != nil {
			return ""
		}
		next, err := url.Parse(raw)
		if err != nil {
			return ""
		}
		return base.ResolveReference(next).String()
	}
	return ""
}

func (c Checker) dockerBearerToken(ctx context.Context, challenge, path string) (string, error) {
	if !strings.HasPrefix(strings.ToLower(challenge), "bearer ") {
		return "", fmt.Errorf("docker registry requires unsupported auth challenge")
	}
	params := parseAuthChallenge(challenge[len("Bearer "):])
	realm := params["realm"]
	if realm == "" {
		return "", fmt.Errorf("docker registry auth challenge missing realm")
	}
	q := url.Values{}
	if service := params["service"]; service != "" {
		q.Set("service", service)
	}
	if scope := params["scope"]; scope != "" {
		q.Set("scope", scope)
	} else {
		q.Set("scope", "repository:"+path+":pull")
	}
	u := realm + "?" + q.Encode()
	var payload struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := c.getJSON(ctx, u, &payload); err != nil {
		return "", err
	}
	if payload.Token != "" {
		return payload.Token, nil
	}
	if payload.AccessToken != "" {
		return payload.AccessToken, nil
	}
	return "", fmt.Errorf("docker auth response contained no token")
}

func splitDockerRepository(repo string) (registry, path string) {
	parts := strings.Split(repo, "/")
	if len(parts) > 1 && (strings.Contains(parts[0], ".") || strings.Contains(parts[0], ":") || parts[0] == "localhost") {
		return parts[0], strings.Join(parts[1:], "/")
	}
	if len(parts) == 1 {
		return "registry-1.docker.io", "library/" + repo
	}
	return "registry-1.docker.io", repo
}

func newestTag(tags []string, pattern string) string {
	var re *regexp.Regexp
	if pattern != "" {
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return ""
		}
		re = compiled
	}
	var candidates []string
	for _, tag := range tags {
		if re != nil && !re.MatchString(tag) {
			continue
		}
		if NormalizeVersion(tag) == "" {
			continue
		}
		candidates = append(candidates, tag)
	}
	sort.Slice(candidates, func(i, j int) bool {
		return CompareVersions(NormalizeVersion(candidates[i]), NormalizeVersion(candidates[j])) > 0
	})
	if len(candidates) == 0 {
		return ""
	}
	return candidates[0]
}

func sourceURL(source manifest.UpdateSource) string {
	switch source.Type {
	case "pypi":
		return "https://pypi.org/pypi/" + source.Package + "/json"
	case "npm":
		return "https://registry.npmjs.org/" + source.Package
	case "github_releases":
		return "https://api.github.com/repos/" + source.Owner + "/" + source.Repo + "/releases/latest"
	case "docker_registry":
		return "docker://" + source.Repository
	case "rubygems":
		return "https://rubygems.org/api/v1/gems/" + source.Package + ".json"
	case "go_module":
		return "https://proxy.golang.org/" + source.Module + "/@v/list"
	case "packagist":
		return "https://repo.packagist.org/p2/" + source.Package + ".json"
	default:
		return ""
	}
}

func parseAuthChallenge(s string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(s, ",") {
		key, val, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		out[strings.ToLower(key)] = strings.Trim(val, `"`)
	}
	return out
}

func NormalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "refs/tags/")
	v = strings.TrimPrefix(v, "pmd_releases/")
	v = strings.TrimPrefix(v, "v")
	end := len(v)
	for i, r := range v {
		if (r >= '0' && r <= '9') || r == '.' {
			continue
		}
		end = i
		break
	}
	return strings.Trim(v[:end], ".")
}

func CompareVersions(a, b string) int {
	aa := versionParts(NormalizeVersion(a))
	bb := versionParts(NormalizeVersion(b))
	max := len(aa)
	if len(bb) > max {
		max = len(bb)
	}
	for i := 0; i < max; i++ {
		var av, bv int
		if i < len(aa) {
			av = aa[i]
		}
		if i < len(bb) {
			bv = bb[i]
		}
		if av > bv {
			return 1
		}
		if av < bv {
			return -1
		}
	}
	return 0
}

func versionParts(v string) []int {
	if v == "" {
		return nil
	}
	raw := strings.Split(v, ".")
	out := make([]int, 0, len(raw))
	for _, part := range raw {
		n, err := strconv.Atoi(part)
		if err != nil {
			break
		}
		out = append(out, n)
	}
	return out
}

func escapeGoModulePath(module string) string {
	var b strings.Builder
	for _, r := range module {
		if r >= 'A' && r <= 'Z' {
			b.WriteByte('!')
			b.WriteRune(r + ('a' - 'A'))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
