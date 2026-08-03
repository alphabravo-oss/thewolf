package ospackages

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ulikunitz/xz"
	"gopkg.in/yaml.v3"
)

const testSnapshot = "20200101T000000Z"

var testCABootstrap = []byte("fixture ca-certificates package")

func TestCompareDebianVersions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		left, right string
		want        int
	}{
		{left: "1.0", right: "1.0", want: 0},
		{left: "1.0~rc1", right: "1.0", want: -1},
		{left: "1:1.0", right: "2.0", want: 1},
		{left: "1.0-1", right: "1.0", want: 1},
		{left: "1.0-0", right: "1.0", want: 0},
		{left: "1.01", right: "1.1", want: 0},
		{left: "1.0+git1", right: "1.0", want: 1},
		{left: "2.0-1~deb13u1", right: "2.0-1", want: -1},
	}
	for _, test := range tests {
		test := test
		t.Run(test.left+"__"+test.right, func(t *testing.T) {
			t.Parallel()
			got := compareDebianVersions(test.left, test.right)
			if got != test.want {
				t.Fatalf("compareDebianVersions(%q, %q)=%d, want %d",
					test.left, test.right, got, test.want)
			}
			reverse := compareDebianVersions(test.right, test.left)
			if reverse != -test.want {
				t.Fatalf("reverse comparison=%d, want %d", reverse, -test.want)
			}
		})
	}
}

func TestRefreshRecordsImmutableIndexesAndExactMultiArchPackages(t *testing.T) {
	t.Parallel()
	policy, policyData, server := refreshFixture(t)
	defer server.Close()

	lock, err := Refresh(context.Background(), policy, policyData, RefreshOptions{
		Snapshot: testSnapshot,
		Client:   server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if lock.Snapshot != testSnapshot || lock.PolicyDigest != PolicyDigest(policyData) {
		t.Fatalf("unexpected snapshot or policy digest: %#v", lock)
	}
	for _, repositoryName := range []string{"debian", "debian-security", "nodesource"} {
		repository := lock.Repositories[repositoryName]
		for _, architecture := range []string{"amd64", "arm64"} {
			index := repository.Indexes[architecture]
			if index.URL == "" || !digestPattern.MatchString(index.SHA256) {
				t.Fatalf("%s/%s index was not locked: %#v", repositoryName, architecture, index)
			}
		}
	}
	if !strings.HasSuffix(lock.Repositories["debian"].Indexes["amd64"].URL, "Packages.xz") {
		t.Fatal("Debian xz index was not selected from Release metadata")
	}
	if !strings.HasSuffix(lock.Repositories["debian-security"].Indexes["amd64"].URL, "Packages.gz") {
		t.Fatal("Debian security gzip index was not selected from Release metadata")
	}
	for platform, lockedPlatform := range lock.Variants["default"].Platforms {
		architecture, _ := platformArchitecture(platform)
		if len(lockedPlatform.Packages) != 3 {
			t.Fatalf("%s package count=%d, want 3", platform, len(lockedPlatform.Packages))
		}
		caPackage := lockedPlatform.Packages[0]
		curlPackage := lockedPlatform.Packages[1]
		nodePackage := lockedPlatform.Packages[2]
		if caPackage.Name != "ca-certificates" || caPackage.Architecture != "all" {
			t.Fatalf("%s does not contain the architecture-independent TLS bootstrap: %#v",
				platform, caPackage)
		}
		if curlPackage.Name != "curl" || curlPackage.Version != "2.0-1" ||
			curlPackage.Source != "debian-security" || curlPackage.Architecture != architecture {
			t.Fatalf("%s did not select the newer security package: %#v", platform, curlPackage)
		}
		if nodePackage.Name != "nodejs" || nodePackage.Source != "nodesource" ||
			nodePackage.Architecture != architecture ||
			!strings.HasPrefix(nodePackage.URL, server.URL+"/nodesource/") {
			t.Fatalf("%s external package is not exact: %#v", platform, nodePackage)
		}
	}
	files, err := RenderFiles(*lock)
	if err != nil {
		t.Fatal(err)
	}
	sources := string(files[filepath.ToSlash(filepath.Join(DefaultOutputDir, "snapshot.sources"))])
	if !strings.Contains(sources, "URIs: https://") ||
		strings.Contains(sources, "URIs: http://") ||
		!strings.Contains(sources, "debian-archive-keyring.pgp") {
		t.Fatalf("generated apt sources are not HTTPS-only:\n%s", sources)
	}
	if !strings.Contains(string(files[filepath.ToSlash(filepath.Join(
		DefaultOutputDir, "pins", "default-amd64.txt"))]), "curl:amd64=2.0-1") {
		t.Fatal("generated amd64 pin is missing exact version")
	}
	bootstrap, err := FetchBootstrapCACertificates(
		context.Background(), lock, server.Client(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bootstrap, testCABootstrap) {
		t.Fatalf("unexpected TLS bootstrap package: %q", bootstrap)
	}
}

func TestCheckIsOfflineAndRejectsStaleOrBypassingInputs(t *testing.T) {
	t.Parallel()
	policy, policyData, server := refreshFixture(t)
	lock, err := Refresh(context.Background(), policy, policyData, RefreshOptions{
		Snapshot: testSnapshot,
		Client:   server.Client(),
	})
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	server.Close()

	root := t.TempDir()
	writeTestFile(t, root, DefaultPolicyPath, policyData)
	lockData, err := lock.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, DefaultLockPath, lockData)
	generated, err := RenderFiles(*lock)
	if err != nil {
		t.Fatal(err)
	}
	for relative, data := range generated {
		writeTestFile(t, root, relative, data)
	}
	writeTestFile(t, root, BootstrapPackagePath, testCABootstrap)
	writeTestFile(t, root, "scanners/Dockerfile", []byte(
		"FROM scratch\nRUN /usr/local/bin/install-os-packages default\n"))
	writeTestFile(t, root, "scanners/install/os-packages.sh", []byte(
		"#!/bin/sh\n# --proto '=https'\n# --proto-redir '=https'\n"+
			"# Acquire::https::CaInfo \"/etc/ssl/certs/ca-certificates.crt\"\n",
	))

	if err := Check(root); err != nil {
		t.Fatalf("offline check failed after fixture server closed: %v", err)
	}

	pinPath := filepath.Join(DefaultOutputDir, "pins", "default-amd64.txt")
	writeTestFile(t, root, pinPath, []byte("# stale\n"))
	if err := Check(root); err == nil || !strings.Contains(err.Error(), "is stale") {
		t.Fatalf("stale generated pin was accepted: %v", err)
	}
	writeTestFile(t, root, pinPath, generated[filepath.ToSlash(pinPath)])
	writeTestFile(t, root, BootstrapPackagePath, []byte("tampered"))
	if err := Check(root); err == nil || !strings.Contains(err.Error(), "digest=") {
		t.Fatalf("tampered TLS bootstrap package was accepted: %v", err)
	}
	writeTestFile(t, root, BootstrapPackagePath, testCABootstrap)
	writeTestFile(t, root, "scanners/Dockerfile", []byte(
		"FROM scratch\nRUN /usr/local/bin/install-os-packages default\nRUN apt-get install curl\n"))
	if err := Check(root); err == nil || !strings.Contains(err.Error(), "direct apt install") {
		t.Fatalf("Dockerfile apt bypass was accepted: %v", err)
	}
}

func TestLockRejectsExternalPackageURLOutsideRepository(t *testing.T) {
	t.Parallel()
	policy, policyData, server := refreshFixture(t)
	defer server.Close()
	lock, err := Refresh(context.Background(), policy, policyData, RefreshOptions{
		Snapshot: testSnapshot,
		Client:   server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	platform := lock.Variants["default"].Platforms["linux/amd64"]
	platform.Packages[2].URL = "https://example.invalid/node.deb"
	lock.Variants["default"].Platforms["linux/amd64"] = platform
	if err := lock.SetDigest(); err != nil {
		t.Fatal(err)
	}
	if err := lock.Validate(); err == nil || !strings.Contains(err.Error(), "invalid URL") {
		t.Fatalf("off-origin external artifact URL was accepted: %v", err)
	}
}

func refreshFixture(t *testing.T) (*Policy, []byte, *httptest.Server) {
	t.Helper()
	var releaseAttempts atomic.Int32
	files := map[string][]byte{}
	caFilename := "pool/main/c/ca-certificates/ca-certificates_1_all.deb"
	caDigest := sha256.Sum256(testCABootstrap)
	caRecord := packageRecordTextWithDigest(
		"ca-certificates", "1", "all", caFilename, hex.EncodeToString(caDigest[:]),
	)
	files["/debian/"+testSnapshot+"/"+caFilename] = testCABootstrap
	for _, architecture := range []string{"amd64", "arm64"} {
		mainIndex := xzTestData(t, append(
			append([]byte{}, caRecord...),
			packageRecordText(
				"curl", "1.0-1", architecture, "pool/c/curl_"+architecture+".deb", "1",
			)...,
		))
		mainPath := "main/binary-" + architecture + "/Packages.xz"
		files["/debian/"+testSnapshot+"/dists/trixie/"+mainPath] = mainIndex
		files["/debian/"+testSnapshot+"/dists/trixie/Release"] = releaseData(mainPath, mainIndex)

		securityIndex := gzipTestData(t, packageRecordText(
			"curl", "2.0-1", architecture, "pool/c/curl-security_"+architecture+".deb", "2",
		))
		securityPath := "main/binary-" + architecture + "/Packages.gz"
		files["/security/"+testSnapshot+"/dists/trixie-security/"+securityPath] = securityIndex
		existingRelease := files["/security/"+testSnapshot+"/dists/trixie-security/Release"]
		files["/security/"+testSnapshot+"/dists/trixie-security/Release"] =
			appendReleaseData(existingRelease, securityPath, securityIndex)

		nodeIndex := gzipTestData(t, packageRecordText(
			"nodejs", "22.1.0-1nodesource1", architecture,
			"pool/main/n/nodejs/nodejs_"+architecture+".deb", "3",
		))
		files["/nodesource/dists/nodistro/main/binary-"+architecture+"/Packages.gz"] = nodeIndex

		// The Debian Release needs both architecture entries.
		if architecture == "arm64" {
			amd64Path := "main/binary-amd64/Packages.xz"
			amd64Index := files["/debian/"+testSnapshot+"/dists/trixie/"+amd64Path]
			files["/debian/"+testSnapshot+"/dists/trixie/Release"] =
				appendReleaseData(releaseData(amd64Path, amd64Index), mainPath, mainIndex)
		}
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/debian/"+testSnapshot+"/dists/trixie/Release" &&
			releaseAttempts.Add(1) == 1 {
			http.Error(writer, "transient", http.StatusServiceUnavailable)
			return
		}
		data, ok := files[request.URL.Path]
		if !ok {
			http.NotFound(writer, request)
			return
		}
		_, _ = writer.Write(data)
	}))
	policy := &Policy{
		SchemaVersion: PolicySchemaVersion,
		Repositories: map[string]RepositoryPolicy{
			"debian": {
				Type: RepositoryDebianSnapshot, URI: server.URL + "/debian",
				Suite: "trixie", Component: "main",
			},
			"debian-security": {
				Type: RepositoryDebianSnapshot, URI: server.URL + "/security",
				Suite: "trixie-security", Component: "main",
			},
			"nodesource": {
				Type: RepositoryAPTArtifact, URI: server.URL + "/nodesource",
				Suite: "nodistro", Component: "main",
			},
		},
		Variants: map[string]VariantPolicy{
			"default": {
				Dockerfile: "scanners/Dockerfile",
				Platforms:  []string{"linux/amd64", "linux/arm64"},
				Packages: map[string][]string{
					"debian":     {"ca-certificates", "curl"},
					"nodesource": {"nodejs"},
				},
			},
		},
	}
	policyData, err := yaml.Marshal(policy)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	return policy, policyData, server
}

func packageRecordText(name, version, architecture, filename, digestCharacter string) []byte {
	return packageRecordTextWithDigest(
		name, version, architecture, filename, strings.Repeat(digestCharacter, 64),
	)
}

func packageRecordTextWithDigest(name, version, architecture, filename, digest string) []byte {
	return []byte(fmt.Sprintf(
		"Package: %s\nVersion: %s\nArchitecture: %s\nFilename: %s\nSHA256: %s\n\n",
		name, version, architecture, filename, digest,
	))
}

func releaseData(indexPath string, index []byte) []byte {
	return appendReleaseData(nil, indexPath, index)
}

func appendReleaseData(current []byte, indexPath string, index []byte) []byte {
	if len(current) == 0 {
		current = []byte("Suite: test\nSHA256:\n")
	}
	sum := sha256.Sum256(index)
	return append(current, []byte(fmt.Sprintf(
		" %s %d %s\n", hex.EncodeToString(sum[:]), len(index), indexPath,
	))...)
}

func gzipTestData(t *testing.T, input []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := gzip.NewWriter(&output)
	if _, err := writer.Write(input); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func xzTestData(t *testing.T, input []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	writer, err := xz.NewWriter(&output)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(input); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func writeTestFile(t *testing.T, root, relative string, data []byte) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
