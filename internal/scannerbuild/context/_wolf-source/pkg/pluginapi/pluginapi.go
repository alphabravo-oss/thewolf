package pluginapi

import "context"

// Kind separates scanner language packs from later intelligence, integration,
// and exporter plugins. Community still compiles first-party scanners.
const (
	KindScanner      = "scanner"
	KindLanguage     = "language"
	KindIntelligence = "intelligence"
	KindIntegration  = "integration"
	KindExporter     = "exporter"
)

func Kinds() []string {
	return []string{KindScanner, KindLanguage, KindIntelligence, KindIntegration, KindExporter}
}

// Plugin is the public scanner contract. Implementations remain under
// plugins/ and internal/plugin. Enterprise private catalogs register extra
// names through pkg/scannerreleaseapi, not by forking this interface.
type Plugin interface {
	Name() string
	CheckAvailable() bool
	Execute(ctx context.Context, repoPath string) error
}
