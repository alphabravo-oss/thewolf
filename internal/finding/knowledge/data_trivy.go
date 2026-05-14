package knowledge

// trivy vulnerability + IaC misconfig mappings. Trivy emits CVE IDs and
// rule IDs of the form "AVD-DKR-####", "AVD-KSV-####" (Kubernetes), etc.
//
// For CVEs we don't enumerate individual IDs — instead categorize *all*
// vulnerability findings as "vulnerable-dependency" via the Categorize
// helper in the runner that falls back when the rule_id starts with "CVE-".

func init() {
	for _, e := range trivyEntries() {
		RegisterEntry(e)
	}
}

func trivyEntries() []Entry {
	const t = "trivy"
	return []Entry{
		// Docker (AVD-DKR-####)
		{Tool: t, RuleID: "AVD-DS-0001", FineCategory: "container-misconfig", FixStrategy: "pin-base-image-digest"},
		{Tool: t, RuleID: "AVD-DS-0002", FineCategory: "container-misconfig", FixStrategy: "non-root-container-user"},
		{Tool: t, RuleID: "AVD-DS-0026", FineCategory: "container-misconfig", FixStrategy: "tool-docs-fallback"},

		// Kubernetes Security Validations (AVD-KSV-####)
		{Tool: t, RuleID: "AVD-KSV-0001", FineCategory: "container-misconfig", FixStrategy: "tool-docs-fallback"},
		{Tool: t, RuleID: "AVD-KSV-0003", FineCategory: "container-misconfig", FixStrategy: "tool-docs-fallback"},
		{Tool: t, RuleID: "AVD-KSV-0011", FineCategory: "container-misconfig", FixStrategy: "tool-docs-fallback"},
		{Tool: t, RuleID: "AVD-KSV-0012", FineCategory: "container-misconfig", FixStrategy: "non-root-container-user"},
		{Tool: t, RuleID: "AVD-KSV-0014", FineCategory: "container-misconfig", FixStrategy: "tool-docs-fallback"},
		{Tool: t, RuleID: "AVD-KSV-0016", FineCategory: "container-misconfig", FixStrategy: "tool-docs-fallback"},
		{Tool: t, RuleID: "AVD-KSV-0020", FineCategory: "container-misconfig", FixStrategy: "tool-docs-fallback"},
		{Tool: t, RuleID: "AVD-KSV-0021", FineCategory: "container-misconfig", FixStrategy: "tool-docs-fallback"},

		// Generic / secret rules in IaC scans
		{Tool: t, RuleID: "AVD-AWS-0001", FineCategory: "iac-misconfig", FixStrategy: "tool-docs-fallback"},
	}
}
