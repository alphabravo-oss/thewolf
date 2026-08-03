// Package scanners embeds the canonical scanner tool manifest (tools.yaml) so
// the wolf binary can load it without a repo checkout on disk. The runtime
// container image ships only the binary + UI, so a file-based load fails there;
// the embedded copy is the always-present fallback. See
// internal/scannertools/manifest.LoadDefault.
package scanners

import _ "embed"

// ToolsYAML is the embedded contents of scanners/tools.yaml.
//
//go:embed tools.yaml
var ToolsYAML []byte
