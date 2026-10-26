package runner

import (
	"context"
	"fmt"
	"sync"
)

// Externally registered scanners (pkg/scannerapi) declare their scheduling and
// secrets here; first-party tools keep using the scanner manifest.
var (
	externalResources sync.Map // tool -> ResourceSpec
	externalSecrets   sync.Map // tool -> map[env]secretName
)

// RegisterExternalResources sets the resource spec of an external scanner. A
// manifest entry for the same tool still wins.
func RegisterExternalResources(tool string, spec ResourceSpec) { externalResources.Store(tool, spec) }

// RegisterExternalSecrets maps container environment names to Wolf secret names
// that must be resolved before the scanner runs.
func RegisterExternalSecrets(tool string, envToSecret map[string]string) {
	copied := make(map[string]string, len(envToSecret))
	for env, name := range envToSecret {
		copied[env] = name
	}
	externalSecrets.Store(tool, copied)
}

func externalResourceSpec(tool string) (ResourceSpec, bool) {
	v, ok := externalResources.Load(tool)
	if !ok {
		return ResourceSpec{}, false
	}
	return v.(ResourceSpec), true
}

// resolveExternalSecrets returns the declared secrets for tool. A declared but
// unavailable secret fails the tool rather than running it unlicensed.
func resolveExternalSecrets(ctx context.Context, tool string, resolve func(context.Context, string) (string, error)) (map[string]string, error) {
	v, ok := externalSecrets.Load(tool)
	if !ok {
		return nil, nil
	}
	declared := v.(map[string]string)
	if len(declared) == 0 {
		return nil, nil
	}
	if resolve == nil {
		return nil, fmt.Errorf("%s requires stored secrets, but this run has no secret resolver", tool)
	}
	out := make(map[string]string, len(declared))
	for env, name := range declared {
		value, err := resolve(ctx, name)
		if err != nil || value == "" {
			return nil, fmt.Errorf("%s requires the secret %q (add it as a custom secret named %q): %v", tool, name, name, err)
		}
		out[env] = value
	}
	return out, nil
}
