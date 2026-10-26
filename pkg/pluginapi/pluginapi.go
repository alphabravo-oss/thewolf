package pluginapi

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

// The scanner contract itself is pkg/scannerapi (Plugin, Register, Run,
// FindingsFromSARIF); this package only names plugin kinds.
