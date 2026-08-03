package openapi

import (
	"encoding/json"
	"testing"
)

func TestRemoteScanHeadersAndConflictAreDocumented(t *testing.T) {
	var spec map[string]interface{}
	if err := json.Unmarshal(SpecJSON(), &spec); err != nil {
		t.Fatal(err)
	}
	paths := spec["paths"].(map[string]interface{})
	create := paths["/scans"].(map[string]interface{})["post"].(map[string]interface{})
	parameters := create["parameters"].([]interface{})
	if parameters[0].(map[string]interface{})["name"] != "Idempotency-Key" {
		t.Fatalf("POST /scans parameters = %#v", parameters)
	}
	if _, ok := create["responses"].(map[string]interface{})["409"]; !ok {
		t.Fatal("POST /scans does not document idempotency conflict")
	}

	stream := paths["/scans/{id}/stream"].(map[string]interface{})["get"].(map[string]interface{})
	streamParameters := stream["parameters"].([]interface{})
	if streamParameters[0].(map[string]interface{})["name"] != "Last-Event-ID" {
		t.Fatalf("GET /scans/{id}/stream parameters = %#v", streamParameters)
	}
}

func TestCreateScanSchemaIncludesRemoteSourceAndScopeFields(t *testing.T) {
	var spec map[string]interface{}
	if err := json.Unmarshal(SpecJSON(), &spec); err != nil {
		t.Fatal(err)
	}
	components := spec["components"].(map[string]interface{})
	schemas := components["schemas"].(map[string]interface{})
	create := schemas["CreateScanRequest"].(map[string]interface{})
	properties := create["properties"].(map[string]interface{})
	for _, field := range []string{
		"repo_id", "source", "profile", "categories", "tools", "disabled_tools",
		"include_paths", "exclude_paths", "client_reference",
	} {
		if _, ok := properties[field]; !ok {
			t.Errorf("CreateScanRequest is missing %q", field)
		}
	}
}

func TestScannerPolicyScheduleIsExplicitAndSharedByUpdateAndDryRun(t *testing.T) {
	var spec map[string]interface{}
	if err := json.Unmarshal(SpecJSON(), &spec); err != nil {
		t.Fatal(err)
	}
	schemas := spec["components"].(map[string]interface{})["schemas"].(map[string]interface{})
	for _, requestName := range []string{"ScannerPolicyRequest", "ScannerPolicyDryRunRequest"} {
		request := schemas[requestName].(map[string]interface{})
		schedule := request["properties"].(map[string]interface{})["schedule"].(map[string]interface{})
		if schedule["$ref"] != "#/components/schemas/ScannerPolicySchedule" {
			t.Fatalf("%s schedule = %#v", requestName, schedule)
		}
	}

	schedule := schemas["ScannerPolicySchedule"].(map[string]interface{})
	if schedule["additionalProperties"] != false {
		t.Fatalf("schedule must reject unknown fields: %#v", schedule)
	}
	properties := schedule["properties"].(map[string]interface{})
	for _, field := range []string{
		"timezone", "daily_discovery", "weekly_candidate",
		"maximum_stable_image_age", "force_weekly_rebuild", "maintenance_windows",
	} {
		if _, ok := properties[field]; !ok {
			t.Errorf("ScannerPolicySchedule is missing %q", field)
		}
	}
	maximumAge := properties["maximum_stable_image_age"].(map[string]interface{})
	if maximumAge["default"] != "168h0m0s" {
		t.Fatalf("maximum stable age contract = %#v", maximumAge)
	}
	windows := properties["maintenance_windows"].(map[string]interface{})
	if windows["maxItems"] != float64(32) {
		t.Fatalf("maintenance window bound = %#v", windows)
	}
	weekly := properties["weekly_candidate"].(map[string]interface{})
	weekdays := weekly["properties"].(map[string]interface{})["weekday"].(map[string]interface{})["enum"].([]interface{})
	if len(weekdays) != 7 {
		t.Fatalf("weekday enum = %#v", weekdays)
	}
}

func TestScannerReleaseBundleTransferIsDocumentedAsStreamingAndTrustAware(t *testing.T) {
	var spec map[string]interface{}
	if err := json.Unmarshal(SpecJSON(), &spec); err != nil {
		t.Fatal(err)
	}
	paths := spec["paths"].(map[string]interface{})

	export := paths["/scanner-supply-chain/releases/{id}/export"].(map[string]interface{})["get"].(map[string]interface{})
	exportResponses := export["responses"].(map[string]interface{})
	exportContent := exportResponses["200"].(map[string]interface{})["content"].(map[string]interface{})
	if _, ok := exportContent["application/vnd.wolf.scanner-release-bundle.v1+tar+zstd"]; !ok {
		t.Fatalf("release export content = %#v", exportContent)
	}
	if _, ok := exportContent["application/vnd.wolf.scanner-release-bundle.v2+tar+zstd"]; !ok {
		t.Fatalf("release export v2 content = %#v", exportContent)
	}

	importOperation := paths["/scanner-supply-chain/release-imports"].(map[string]interface{})["post"].(map[string]interface{})
	importContent := importOperation["requestBody"].(map[string]interface{})["content"].(map[string]interface{})
	bundle := importContent["application/vnd.wolf.scanner-release-bundle.v1+tar+zstd"].(map[string]interface{})
	if bundle["schema"].(map[string]interface{})["format"] != "binary" {
		t.Fatalf("release import schema = %#v", bundle)
	}
	parameters := importOperation["parameters"].([]interface{})
	names := map[string]bool{}
	for _, raw := range parameters {
		names[raw.(map[string]interface{})["name"].(string)] = true
	}
	for _, name := range []string{
		"Idempotency-Key", "X-Wolf-Import-Reason", "allow_unverified",
		"registry_target_id", "no_network",
	} {
		if !names[name] {
			t.Errorf("release import is missing %s", name)
		}
	}
	responses := importOperation["responses"].(map[string]interface{})
	for _, code := range []string{"200", "201", "409", "413", "415", "422", "428"} {
		if _, ok := responses[code]; !ok {
			t.Errorf("release import is missing response %s", code)
		}
	}
	success := responses["201"].(map[string]interface{})
	successSchema := success["content"].(map[string]interface{})["application/json"].(map[string]interface{})["schema"].(map[string]interface{})
	if successSchema["$ref"] != "#/components/schemas/ScannerReleaseBundleImportResponse" {
		t.Fatalf("release import response schema = %#v", successSchema)
	}
	schemas := spec["components"].(map[string]interface{})["schemas"].(map[string]interface{})
	result := schemas["ScannerReleaseBundleImportResult"].(map[string]interface{})
	properties := result["properties"].(map[string]interface{})
	for _, field := range []string{
		"bundle_schema", "oci_closure_verified", "network_mode",
		"destination_read_back_verified", "registry_mappings",
	} {
		if _, ok := properties[field]; !ok {
			t.Errorf("bundle import result is missing %s", field)
		}
	}
}

func TestScannerReleaseOverviewDocumentsCapabilityStages(t *testing.T) {
	var spec map[string]interface{}
	if err := json.Unmarshal(SpecJSON(), &spec); err != nil {
		t.Fatal(err)
	}
	paths := spec["paths"].(map[string]interface{})
	overview := paths["/scanner-supply-chain/overview"].(map[string]interface{})["get"].(map[string]interface{})
	response := overview["responses"].(map[string]interface{})["200"].(map[string]interface{})
	schema := response["content"].(map[string]interface{})["application/json"].(map[string]interface{})["schema"].(map[string]interface{})
	if schema["$ref"] != "#/components/schemas/ScannerSupplyChainOverviewResponse" {
		t.Fatalf("overview response schema = %#v", schema)
	}

	components := spec["components"].(map[string]interface{})
	schemas := components["schemas"].(map[string]interface{})
	capabilities := schemas["ScannerReleaseCapabilities"].(map[string]interface{})
	properties := capabilities["properties"].(map[string]interface{})
	for _, field := range []string{"mode", "read", "candidates", "canary", "stable_control"} {
		if _, ok := properties[field]; !ok {
			t.Errorf("ScannerReleaseCapabilities is missing %q", field)
		}
	}
	modes := properties["mode"].(map[string]interface{})["enum"].([]interface{})
	if len(modes) != 5 || modes[0] != "disabled" || modes[4] != "stable_control" {
		t.Fatalf("capability modes = %#v", modes)
	}
}

func TestScannerRolloutDetailSeparatesSyntheticAndRealScanHealth(t *testing.T) {
	var spec map[string]interface{}
	if err := json.Unmarshal(SpecJSON(), &spec); err != nil {
		t.Fatal(err)
	}
	paths := spec["paths"].(map[string]interface{})
	operation := paths["/scanner-supply-chain/rollouts/{id}"].(map[string]interface{})["get"].(map[string]interface{})
	success := operation["responses"].(map[string]interface{})["200"].(map[string]interface{})
	schema := success["content"].(map[string]interface{})["application/json"].(map[string]interface{})["schema"].(map[string]interface{})
	if schema["$ref"] != "#/components/schemas/ScannerRolloutDetailResponse" {
		t.Fatalf("rollout detail response schema = %#v", schema)
	}
	schemas := spec["components"].(map[string]interface{})["schemas"].(map[string]interface{})
	for name, fields := range map[string][]string{
		"ScannerSyntheticHealth": {
			"corpus_id", "corpus_digest", "current", "state",
			"fixture_total", "fixture_passed", "fixture_failed",
			"failure_class", "observed_at",
		},
		"ScannerRealScanHealth": {
			"state", "candidate_samples", "stable_samples",
			"candidate_infrastructure_failures", "stable_infrastructure_failures",
			"parser_failures", "expected_finding_losses",
			"candidate_p95_duration_ms", "stable_p95_duration_ms",
			"workers_total", "workers_ready", "workers_failed", "observed_at",
		},
	} {
		properties := schemas[name].(map[string]interface{})["properties"].(map[string]interface{})
		for _, field := range fields {
			if _, ok := properties[field]; !ok {
				t.Errorf("%s is missing %q", name, field)
			}
		}
	}
	detail := schemas["ScannerRolloutDetailResponse"].(map[string]interface{})
	data := detail["properties"].(map[string]interface{})["data"].(map[string]interface{})
	properties := data["properties"].(map[string]interface{})
	for _, field := range []string{"synthetic_health", "real_scan_health"} {
		if _, ok := properties[field]; !ok {
			t.Errorf("rollout detail is missing %q", field)
		}
	}
}

func TestScannerReleaseContractsClassifyCompleteScannerAndFixerImages(t *testing.T) {
	var spec map[string]interface{}
	if err := json.Unmarshal(SpecJSON(), &spec); err != nil {
		t.Fatal(err)
	}
	schemas := spec["components"].(map[string]interface{})["schemas"].(map[string]interface{})
	pipeline := schemas["ScannerPipelineImage"].(map[string]interface{})
	pipelineProperties := pipeline["properties"].(map[string]interface{})
	kinds := pipelineProperties["kind"].(map[string]interface{})["enum"].([]interface{})
	if len(kinds) != 2 || kinds[0] != "scanner" || kinds[1] != "fixer" {
		t.Fatalf("pipeline image kinds = %#v", kinds)
	}
	keys := pipelineProperties["key"].(map[string]interface{})["enum"].([]interface{})
	if len(keys) != 8 {
		t.Fatalf("canonical pipeline image keys = %#v", keys)
	}
	release := schemas["ScannerReleaseImage"].(map[string]interface{})
	if _, exists := release["properties"].(map[string]interface{})["image_kind"]; !exists {
		t.Fatal("release image schema does not expose image_kind")
	}
	candidate := schemas["ScannerCandidateRequest"].(map[string]interface{})
	candidateRequired := candidate["required"].([]interface{})
	if len(candidateRequired) != 1 || candidateRequired[0] != "reason" {
		t.Fatalf("candidate required fields = %#v", candidateRequired)
	}
	images := candidate["properties"].(map[string]interface{})["images"].(map[string]interface{})
	if images["minItems"] != float64(8) || images["maxItems"] != float64(8) {
		t.Fatalf("candidate image cardinality = %#v", images)
	}
}

func TestScannerNotificationOperationsDocumentFiltersAndRetryPreconditions(t *testing.T) {
	var spec map[string]interface{}
	if err := json.Unmarshal(SpecJSON(), &spec); err != nil {
		t.Fatal(err)
	}
	paths := spec["paths"].(map[string]interface{})
	list := paths["/scanner-supply-chain/notifications"].(map[string]interface{})["get"].(map[string]interface{})
	filterNames := map[string]bool{}
	for _, raw := range list["parameters"].([]interface{}) {
		filterNames[raw.(map[string]interface{})["name"].(string)] = true
	}
	for _, name := range []string{
		"state", "destination_type", "notification_type", "cursor", "limit",
	} {
		if !filterNames[name] {
			t.Errorf("notification list is missing %s filter", name)
		}
	}

	retry := paths["/scanner-supply-chain/notifications/{id}/retry"].(map[string]interface{})["post"].(map[string]interface{})
	headerNames := map[string]bool{}
	for _, raw := range retry["parameters"].([]interface{}) {
		headerNames[raw.(map[string]interface{})["name"].(string)] = true
	}
	for _, name := range []string{"Idempotency-Key", "If-Match"} {
		if !headerNames[name] {
			t.Errorf("notification retry is missing %s", name)
		}
	}
	if _, ok := retry["responses"].(map[string]interface{})["200"]; !ok {
		t.Fatal("notification retry does not document synchronous 200 response")
	}
}

func TestScannerArtifactDiffEndpointsDocumentBoundedAuthenticatedContent(t *testing.T) {
	var spec map[string]interface{}
	if err := json.Unmarshal(SpecJSON(), &spec); err != nil {
		t.Fatal(err)
	}
	paths := spec["paths"].(map[string]interface{})
	for _, path := range []string{
		"/scanner-supply-chain/candidates/{id}/diffs/{kind}",
		"/scanner-supply-chain/releases/{id}/diffs/{kind}",
	} {
		pathItem := paths[path].(map[string]interface{})
		operation := pathItem["get"].(map[string]interface{})
		if _, ok := operation["security"]; !ok {
			t.Errorf("%s does not require authentication", path)
		}
		parameters := pathItem["parameters"].([]interface{})
		kind := parameters[1].(map[string]interface{})
		values := kind["schema"].(map[string]interface{})["enum"].([]interface{})
		if len(values) != 2 || values[0] != "manifest" || values[1] != "lock" {
			t.Errorf("%s kind enum = %#v", path, values)
		}
		responses := operation["responses"].(map[string]interface{})
		for _, code := range []string{"200", "401", "403", "404", "409", "413", "415", "422", "503"} {
			if _, ok := responses[code]; !ok {
				t.Errorf("%s is missing response %s", path, code)
			}
		}
		success := responses["200"].(map[string]interface{})
		schema := success["content"].(map[string]interface{})["application/json"].(map[string]interface{})["schema"].(map[string]interface{})
		if schema["$ref"] != "#/components/schemas/ScannerArtifactDiffResponse" {
			t.Errorf("%s response schema = %#v", path, schema)
		}
	}

	schemas := spec["components"].(map[string]interface{})["schemas"].(map[string]interface{})
	diff := schemas["ScannerArtifactDiff"].(map[string]interface{})
	properties := diff["properties"].(map[string]interface{})
	content := properties["content"].(map[string]interface{})
	if content["maxLength"] != float64(maxScannerArtifactDiffResponseCharacters) {
		t.Fatalf("diff content bound = %#v", content["maxLength"])
	}
	for _, field := range []string{
		"available", "content", "truncated", "total_bytes", "returned_bytes",
		"total_lines", "returned_lines", "digest", "media_type",
	} {
		if _, ok := properties[field]; !ok {
			t.Errorf("ScannerArtifactDiff is missing %q", field)
		}
	}
}

func TestLegacyScannerBuildEndpointsDocumentDeprecationAndGoneResponse(t *testing.T) {
	var spec map[string]interface{}
	if err := json.Unmarshal(SpecJSON(), &spec); err != nil {
		t.Fatal(err)
	}
	paths := spec["paths"].(map[string]interface{})
	for _, path := range []string{
		"/scanners/images/{variant}/build",
		"/scanners/images/build-all",
	} {
		operation := paths[path].(map[string]interface{})["post"].(map[string]interface{})
		if operation["deprecated"] != true {
			t.Errorf("%s is not marked deprecated", path)
		}
		responses := operation["responses"].(map[string]interface{})
		for _, code := range []string{"200", "410", "503"} {
			if _, ok := responses[code]; !ok {
				t.Errorf("%s is missing response %s", path, code)
			}
		}
		for _, code := range []string{"200", "410"} {
			headers := responses[code].(map[string]interface{})["headers"].(map[string]interface{})
			if _, ok := headers["Deprecation"]; !ok {
				t.Errorf("%s response %s is missing Deprecation header", path, code)
			}
			if _, ok := headers["Link"]; !ok {
				t.Errorf("%s response %s is missing Link header", path, code)
			}
		}
	}
}
