package scannerreleaseadapter

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/scannerreleaseworkspace"
)

type fakeORASRegistry struct {
	mu        sync.Mutex
	blobs     map[string][]byte
	manifests map[string][]byte
	pushes    int
}

func newFakeORASRegistry() *fakeORASRegistry {
	return &fakeORASRegistry{
		blobs: make(map[string][]byte), manifests: make(map[string][]byte),
	}
}

func (f *fakeORASRegistry) execute(
	_ context.Context, _ string, args ...string,
) ([]byte, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(args) < 2 {
		return nil, nil, errors.New("incomplete fake ORAS command")
	}
	switch strings.Join(args[:2], " ") {
	case "blob push":
		if len(args) < 6 {
			return nil, nil, errors.New("invalid blob push")
		}
		value, err := os.ReadFile(args[len(args)-1])
		if err != nil {
			return nil, nil, err
		}
		f.blobs[args[len(args)-2]] = value
		f.pushes++
		return nil, nil, nil
	case "manifest push":
		if len(args) < 6 {
			return nil, nil, errors.New("invalid manifest push")
		}
		value, err := os.ReadFile(args[len(args)-1])
		if err != nil {
			return nil, nil, err
		}
		f.manifests[args[len(args)-2]] = value
		f.pushes++
		return nil, nil, nil
	case "manifest fetch":
		if len(args) == 4 && args[2] == "--descriptor" {
			value, exists := f.manifests[args[3]]
			if !exists {
				return nil, []byte("manifest unknown"), errors.New("not found")
			}
			descriptor, err := json.Marshal(ociDescriptor{
				MediaType: ociManifestMediaType, Digest: sha256Digest(value), Size: int64(len(value)),
			})
			return descriptor, nil, err
		}
		if len(args) != 3 {
			return nil, nil, errors.New("invalid manifest fetch")
		}
		value, exists := f.manifests[args[2]]
		if !exists {
			return nil, []byte("manifest unknown"), errors.New("not found")
		}
		return append([]byte(nil), value...), nil, nil
	case "blob fetch":
		if len(args) != 5 || args[2] != "--output" {
			return nil, nil, errors.New("invalid blob fetch")
		}
		value, exists := f.blobs[args[4]]
		if !exists {
			return nil, []byte("blob unknown"), errors.New("not found")
		}
		return nil, nil, os.WriteFile(args[3], value, 0o600)
	default:
		return nil, nil, errors.New("unexpected fake ORAS command")
	}
}

func TestORASPublisherRecoversAnOperationWithoutRepublishing(t *testing.T) {
	workspace := t.TempDir()
	if err := scannerreleaseworkspace.WriteContext(workspace, scannerreleaseworkspace.ExecutionContext{
		SourceURL: "https://git.example/wolf.git",
		Primary: scannerreleaseworkspace.RegistryTarget{
			ID: "primary", Version: 1, Host: "registry.example", Namespace: "wolf", Repository: "releases",
		},
		Mirror: scannerreleaseworkspace.RegistryTarget{
			ID: "mirror", Version: 1, Host: "mirror.example", Namespace: "wolf", Repository: "releases",
		},
	}); err != nil {
		t.Fatal(err)
	}
	credentials := t.TempDir()
	if err := os.WriteFile(filepath.Join(credentials, "config.json"), []byte(`{"auths":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := newFakeORASRegistry()
	publisher := orasPublisher{
		Path: "/trusted/oras", CredentialDir: credentials, ScratchRoot: t.TempDir(),
		execute: registry.execute,
	}
	request := PublishRequest{
		Workspace: workspace, OperationID: "sha256:" + strings.Repeat("a", 64),
		Action: "manifest-validate", CommandID: "fixed.manifest-validate.v1",
		Payload: []byte(`{"validated":true}`), MediaType: "application/json",
	}
	first, err := publisher.Publish(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	pushes := registry.pushes
	second, err := publisher.Publish(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if registry.pushes != pushes {
		t.Fatalf("recovery republished OCI content: pushes before=%d after=%d", pushes, registry.pushes)
	}
	if first != second || !second.ReadBackVerified {
		t.Fatalf("recovered artifact changed: first=%#v second=%#v", first, second)
	}
}

func TestORASPublisherRejectsAConflictingOperationAlias(t *testing.T) {
	workspace := t.TempDir()
	if err := scannerreleaseworkspace.WriteContext(workspace, scannerreleaseworkspace.ExecutionContext{
		SourceURL: "https://git.example/wolf.git",
		Primary: scannerreleaseworkspace.RegistryTarget{
			ID: "primary", Version: 1, Host: "registry.example", Namespace: "wolf", Repository: "releases",
		},
		Mirror: scannerreleaseworkspace.RegistryTarget{
			ID: "mirror", Version: 1, Host: "mirror.example", Namespace: "wolf", Repository: "releases",
		},
	}); err != nil {
		t.Fatal(err)
	}
	credentials := t.TempDir()
	if err := os.WriteFile(filepath.Join(credentials, "config.json"), []byte(`{"auths":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := newFakeORASRegistry()
	publisher := orasPublisher{
		Path: "/trusted/oras", CredentialDir: credentials, ScratchRoot: t.TempDir(), execute: registry.execute,
	}
	request := PublishRequest{
		Workspace: workspace, OperationID: "sha256:" + strings.Repeat("b", 64),
		Action: "manifest-validate", CommandID: "fixed.manifest-validate.v1",
		Payload: []byte(`{"validated":true}`), MediaType: "application/json",
	}
	if _, err := publisher.Publish(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	repository := "registry.example/releases/wolf-release-evidence"
	alias := repository + ":wolf-op-" + strings.Repeat("b", 64)
	registry.mu.Lock()
	registry.manifests[alias] = []byte(`{"schemaVersion":2,"conflict":true}`)
	registry.mu.Unlock()
	if _, err := publisher.Publish(context.Background(), request); err == nil ||
		!strings.Contains(err.Error(), "conflicting evidence") {
		t.Fatalf("conflicting operation alias error = %v", err)
	}
}
