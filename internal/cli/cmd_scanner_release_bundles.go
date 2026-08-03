package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

const scannerReleaseBundleMediaType = "application/vnd.wolf.scanner-release-bundle.v1+tar+zstd"

func newScannerReleaseExportCommand() *cobra.Command {
	var destination string
	var force bool
	var bundleVersion string
	var platforms []string
	command := &cobra.Command{
		Use:   "export <release-id>",
		Short: "Export a portable immutable release bundle",
		Long: "Export a content-addressed tar.zst bundle without buffering it in memory. " +
			"The completion message reports whether the portable manifest was signed; image-level external signatures are evidence only.",
		Annotations: apiAnno("GET", scannerSupplyChainPath+"/releases/{}/export"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			releaseID := args[0]
			values := url.Values{}
			values.Set("bundle_version", bundleVersion)
			for _, platform := range platforms {
				values.Add("platform", platform)
			}
			path := scannerSupplyChainPath + "/releases/" + url.PathEscape(releaseID) + "/export?" + values.Encode()
			if destination == "" {
				destination = safeReleaseBundleFilename(releaseID) + ".scanner-release.tar.zst"
			}
			if destination == "-" {
				result, err := client.Download(cmd.Context(), path, cmd.OutOrStdout())
				if err != nil {
					return err
				}
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"exported release %s to stdout (%d bytes, bundle %s, manifest %s, signature %s)\n",
					releaseID, result.Bytes, result.BundleDigest, result.ManifestDigest,
					valueOrUnknown(result.SignatureStatus),
				)
				return nil
			}
			result, err := downloadReleaseBundleFile(cmd, client, path, destination, force)
			if err != nil {
				return err
			}
			data, err := json.Marshal(map[string]any{
				"path": destination, "release_id": releaseID, "bytes": result.Bytes,
				"bundle_digest":    result.BundleDigest,
				"manifest_digest":  result.ManifestDigest,
				"signature_status": valueOrUnknown(result.SignatureStatus),
				"bundle_version":   bundleVersion,
				"platforms":        platforms,
			})
			if err != nil {
				return err
			}
			return Render(cmd.OutOrStdout(), resolveOutput(cmd), &Envelope{Data: data})
		},
	}
	command.Flags().StringVar(&destination, "file", "", "destination path (default: <release-id>.scanner-release.tar.zst; use - for stdout)")
	command.Flags().BoolVar(&force, "force", false, "atomically replace an existing destination file")
	command.Flags().StringVar(&bundleVersion, "bundle-version", "2", "portable bundle schema version: 1 metadata-only compatibility or 2 complete OCI transfer")
	command.Flags().StringSliceVar(&platforms, "platform", nil, "OCI platform to include (repeatable; v2 only; default all)")
	return command
}

func newScannerReleaseImportCommand() *cobra.Command {
	var (
		reason          string
		idempotencyKey  string
		allowUnverified bool
		registryTarget  string
		noNetwork       bool
	)
	command := &cobra.Command{
		Use:   "import <bundle-file>",
		Short: "Verify and import a portable scanner release bundle",
		Long: "Stream an offline release bundle to the server. Bundle schema and content digests are always verified. " +
			"Portable signatures are verified only against the server's configured signer trust policy; external image-signature evidence is preserved but is not reported as cryptographically verified.",
		Annotations: apiAnno("POST", scannerSupplyChainPath+"/release-imports"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(reason) == "" {
				return errors.New("--reason is required")
			}
			client, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			var (
				reader io.Reader
				size   int64 = -1
				file   *os.File
			)
			if args[0] == "-" {
				reader = cmd.InOrStdin()
			} else {
				file, err = os.Open(args[0])
				if err != nil {
					return fmt.Errorf("open release bundle: %w", err)
				}
				defer file.Close()
				info, statErr := file.Stat()
				if statErr != nil {
					return fmt.Errorf("inspect release bundle: %w", statErr)
				}
				if !info.Mode().IsRegular() {
					return errors.New("release bundle must be a regular file or - for stdin")
				}
				size = info.Size()
				reader = file
			}
			if idempotencyKey == "" {
				idempotencyKey = uuid.NewString()
			}
			values := url.Values{}
			if allowUnverified {
				values.Set("allow_unverified", "true")
			}
			if registryTarget != "" {
				values.Set("registry_target_id", registryTarget)
			}
			if noNetwork {
				values.Set("no_network", "true")
			}
			path := scannerSupplyChainPath + "/release-imports"
			if encoded := values.Encode(); encoded != "" {
				path += "?" + encoded
			}
			envelope, err := client.Upload(
				cmd.Context(), http.MethodPost, path, scannerReleaseBundleMediaType,
				reader, size, map[string]string{
					"Idempotency-Key":      idempotencyKey,
					"X-Wolf-Import-Reason": reason,
				},
			)
			if err != nil {
				return err
			}
			return Render(cmd.OutOrStdout(), resolveOutput(cmd), envelope)
		},
	}
	command.Flags().StringVar(&reason, "reason", "", "auditable reason for introducing this offline release")
	command.Flags().StringVar(&idempotencyKey, "idempotency-key", "", "stable import key (default: generated UUID)")
	command.Flags().BoolVar(&allowUnverified, "allow-unverified", false, "break-glass import without a trusted portable signature; integrity digests are still verified")
	command.Flags().StringVar(&registryTarget, "registry-target", "", "destination private-registry target ID for digest-idempotent upload and readback")
	command.Flags().BoolVar(&noNetwork, "no-network", false, "forbid registry upload and complete import from bundle contents only")
	return command
}

func downloadReleaseBundleFile(
	cmd *cobra.Command,
	client *Client,
	path, destination string,
	force bool,
) (TransferResult, error) {
	if !force {
		if _, err := os.Lstat(destination); err == nil {
			return TransferResult{}, fmt.Errorf("destination %q already exists (use --force to replace it)", destination)
		} else if !errors.Is(err, os.ErrNotExist) {
			return TransferResult{}, fmt.Errorf("inspect destination: %w", err)
		}
	}
	directory := filepath.Dir(destination)
	temp, err := os.CreateTemp(directory, "."+filepath.Base(destination)+".partial-*")
	if err != nil {
		return TransferResult{}, fmt.Errorf("create temporary destination: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return TransferResult{}, err
	}
	result, err := client.Download(cmd.Context(), path, temp)
	if err != nil {
		_ = temp.Close()
		return TransferResult{}, err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return TransferResult{}, fmt.Errorf("sync downloaded release bundle: %w", err)
	}
	if err := temp.Close(); err != nil {
		return TransferResult{}, fmt.Errorf("close downloaded release bundle: %w", err)
	}
	if force {
		if err := os.Rename(tempPath, destination); err != nil {
			return TransferResult{}, fmt.Errorf("replace release bundle destination: %w", err)
		}
		return result, nil
	}
	if err := os.Link(tempPath, destination); err != nil {
		if errors.Is(err, os.ErrExist) {
			return TransferResult{}, fmt.Errorf("destination %q was created while downloading; no file was replaced", destination)
		}
		return TransferResult{}, fmt.Errorf("install release bundle destination: %w", err)
	}
	return result, nil
}

func safeReleaseBundleFilename(value string) string {
	var result strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			result.WriteRune(r)
		} else {
			result.WriteByte('-')
		}
	}
	if result.Len() == 0 {
		return "scanner-release"
	}
	return result.String()
}

func valueOrUnknown(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return value
}
