package security

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/dast"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

//go:embed zap_runner.py
var zapRunner string

type ZAPPlugin struct{}

func init()                                     { plugin.Register(&ZAPPlugin{}) }
func (*ZAPPlugin) Name() string                 { return "zap" }
func (*ZAPPlugin) Category() models.Category    { return models.CategoryDAST }
func (*ZAPPlugin) Languages() []models.Language { return nil }
func (*ZAPPlugin) CheckAvailable() bool         { return container.IsScannersReady() }
func (*ZAPPlugin) Applicability(_, target string) error {
	if target == "" {
		return &plugin.NotApplicableError{Reason: "no approved repository DAST target"}
	}
	return nil
}

func (*ZAPPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if opts.DAST == nil || !opts.DAST.Target.Approved() {
		return nil, &plugin.NotApplicableError{Reason: "no approved repository DAST target"}
	}
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	proxy, err := dast.StartProxy(ctx, opts.DAST.Target)
	if err != nil {
		return nil, err
	}
	defer proxy.Close()
	payload, err := json.Marshal(map[string]any{"target": opts.DAST.Target, "headers": opts.DAST.Headers, "proxy": proxy.URL})
	if err != nil {
		return nil, err
	}
	if opts.OnOutput != nil {
		opts.OnOutput("ZAP: scanning approved target (" + opts.DAST.Target.Mode + ")")
	}
	cmd := container.CommandContext(ctx, container.ConfigFromOpts(opts.ContainerCfg), container.Options{
		NoRepoMount: true, DASTProxy: true, EntrypointOverride: "python3", Stdin: string(payload), ExtraEnv: map[string]string{"HOME": "/tmp"},
	}, "zap", "-c", zapRunner)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ZAP failed to start or crashed (check scanner image and timeout): %v", err)
	}
	var result struct {
		ExitCode int             `json:"exit_code"`
		Report   json.RawMessage `json:"report"`
	}
	if json.Unmarshal(out, &result) != nil {
		return nil, fmt.Errorf("ZAP returned an invalid execution result")
	}
	if result.ExitCode < 0 || result.ExitCode > 2 {
		return nil, fmt.Errorf("ZAP failed (exit %d): target unreachable, scan timed out, or scanner crashed", result.ExitCode)
	}
	if !proxy.ReachedTarget() {
		return nil, fmt.Errorf("ZAP could not reach the approved target through the DAST proxy")
	}
	// Reflected credentials must not enter raw artifacts or finding evidence.
	for _, secret := range opts.DAST.Headers {
		encoded, _ := json.Marshal(secret)
		if len(encoded) > 2 {
			result.Report = bytes.ReplaceAll(result.Report, encoded[1:len(encoded)-1], []byte("[REDACTED]"))
		}
	}
	findings, err := parseZAPOutput(result.Report)
	if err != nil {
		return nil, err
	}
	plugin.SaveRaw(opts, result.Report, "json")
	return findings, nil
}

type zapAlert struct {
	PluginID    string `json:"pluginid"`
	Name        string `json:"name"`
	Alert       string `json:"alert"`
	Risk        string `json:"riskcode"`
	Confidence  string `json:"confidence"`
	Description string `json:"desc"`
	Solution    string `json:"solution"`
	CWE         string `json:"cweid"`
	WASC        string `json:"wascid"`
	Instances   []struct {
		URI      string `json:"uri"`
		Method   string `json:"method"`
		Param    string `json:"param"`
		Evidence string `json:"evidence"`
	} `json:"instances"`
}

func parseZAPOutput(data []byte) ([]models.Finding, error) {
	var report struct {
		Sites []struct {
			Alerts []zapAlert `json:"alerts"`
		} `json:"site"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("parse ZAP JSON report: %w", err)
	}
	if len(report.Sites) == 0 {
		return nil, fmt.Errorf("ZAP report contains no scanned site; target may be unreachable")
	}
	findings := []models.Finding{}
	severities := []models.Severity{models.SeverityInfo, models.SeverityLow, models.SeverityMedium, models.SeverityHigh}
	for _, site := range report.Sites {
		for _, a := range site.Alerts {
			risk, err := strconv.Atoi(a.Risk)
			if err != nil || risk < 0 || risk >= len(severities) || a.PluginID == "" {
				return nil, fmt.Errorf("ZAP report contains an invalid alert/risk")
			}
			if len(a.Instances) == 0 {
				return nil, fmt.Errorf("ZAP alert %s contains no locations", a.PluginID)
			}
			for _, instance := range a.Instances {
				if _, err := dast.ParseURL(instance.URI); err != nil {
					return nil, fmt.Errorf("ZAP alert %s contains an invalid URL", a.PluginID)
				}
				confidence := "low"
				if a.Confidence == "2" {
					confidence = "medium"
				} else if a.Confidence == "3" || a.Confidence == "4" {
					confidence = "high"
				}
				location := instance.URI + " [" + instance.Method
				if instance.Param != "" {
					location += " param=" + instance.Param
				}
				location += "]"
				evidence, _ := json.Marshal(map[string]any{"pluginid": a.PluginID, "riskcode": a.Risk, "confidence": a.Confidence, "cweid": a.CWE, "wascid": a.WASC, "instance": instance})
				cwe := ""
				if n, _ := strconv.Atoi(a.CWE); n > 0 {
					cwe = "CWE-" + a.CWE
				}
				title := a.Name
				if title == "" {
					title = a.Alert
				}
				findings = append(findings, models.Finding{ToolName: "zap", Category: models.CategoryDAST, Severity: severities[risk], Title: title, Description: strings.TrimSpace(a.Description + "\n\n" + a.Solution), FilePath: location, RuleID: a.PluginID, CWEID: cwe, Confidence: confidence, CodeSnippet: instance.Evidence, SARIFData: string(evidence), Status: models.StatusOpen})
			}
		}
	}
	return findings, nil
}
