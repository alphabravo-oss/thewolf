package scannerreleaseworker

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerpipeline"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

type persistedStepMetadata struct {
	Kind           scannerpipeline.StepKind `json:"kind"`
	DependsOn      []string                 `json:"depends_on"`
	Timeout        string                   `json:"timeout"`
	Retryable      bool                     `json:"retryable"`
	ConcurrencyKey string                   `json:"concurrency_key"`
	Ordinal        int                      `json:"ordinal"`
}

type logicalStep struct {
	Plan     scannerpipeline.Step
	Metadata persistedStepMetadata
	Attempts []scannerrelease.BuildStep
}

func restorePlan(records []scannerrelease.BuildStep) (scannerpipeline.Plan, map[string]*logicalStep, error) {
	if len(records) == 0 {
		return scannerpipeline.Plan{}, nil, fmt.Errorf("build has no persisted steps")
	}
	logical := make(map[string]*logicalStep)
	for _, record := range records {
		var metadata persistedStepMetadata
		if err := json.Unmarshal([]byte(record.SummaryJSON), &metadata); err != nil {
			return scannerpipeline.Plan{}, nil, fmt.Errorf("decode step %q metadata: %w", record.StepKey, err)
		}
		timeout, err := time.ParseDuration(metadata.Timeout)
		if err != nil || timeout <= 0 {
			return scannerpipeline.Plan{}, nil, fmt.Errorf("step %q has invalid timeout %q", record.StepKey, metadata.Timeout)
		}
		if !validStepKind(metadata.Kind) {
			return scannerpipeline.Plan{}, nil, fmt.Errorf(
				"step %q has invalid kind %q", record.StepKey, metadata.Kind,
			)
		}
		step := scannerpipeline.Step{
			Key: record.StepKey, Kind: metadata.Kind, DependsOn: append([]string(nil), metadata.DependsOn...),
			Timeout: timeout, Retryable: metadata.Retryable, Required: true,
			ConcurrencyKey: metadata.ConcurrencyKey,
		}
		existing := logical[record.StepKey]
		if existing == nil {
			existing = &logicalStep{Plan: step, Metadata: metadata}
			logical[record.StepKey] = existing
		} else if !samePlanStep(existing.Plan, step) {
			return scannerpipeline.Plan{}, nil, fmt.Errorf("step %q attempt metadata changed", record.StepKey)
		}
		existing.Attempts = append(existing.Attempts, record)
	}
	ordered := make([]*logicalStep, 0, len(logical))
	for _, step := range logical {
		sort.Slice(step.Attempts, func(i, j int) bool {
			if step.Attempts[i].Attempt == step.Attempts[j].Attempt {
				return step.Attempts[i].CreatedAt.Before(step.Attempts[j].CreatedAt)
			}
			return step.Attempts[i].Attempt < step.Attempts[j].Attempt
		})
		ordered = append(ordered, step)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Metadata.Ordinal == ordered[j].Metadata.Ordinal {
			return ordered[i].Plan.Key < ordered[j].Plan.Key
		}
		return ordered[i].Metadata.Ordinal < ordered[j].Metadata.Ordinal
	})
	plan := scannerpipeline.Plan{Steps: make([]scannerpipeline.Step, 0, len(ordered))}
	for _, step := range ordered {
		plan.Steps = append(plan.Steps, step.Plan)
	}
	if err := plan.Validate(); err != nil {
		return scannerpipeline.Plan{}, nil, err
	}
	return plan, logical, nil
}

func validStepKind(kind scannerpipeline.StepKind) bool {
	switch kind {
	case scannerpipeline.StepCheckout, scannerpipeline.StepValidation,
		scannerpipeline.StepBuild, scannerpipeline.StepTest,
		scannerpipeline.StepSecurity, scannerpipeline.StepEvidence,
		scannerpipeline.StepPublish, scannerpipeline.StepIntegration,
		scannerpipeline.StepPolicy:
		return true
	default:
		return false
	}
}

func samePlanStep(left, right scannerpipeline.Step) bool {
	if left.Key != right.Key || left.Kind != right.Kind || left.Timeout != right.Timeout ||
		left.Retryable != right.Retryable || left.ConcurrencyKey != right.ConcurrencyKey ||
		len(left.DependsOn) != len(right.DependsOn) {
		return false
	}
	for index := range left.DependsOn {
		if left.DependsOn[index] != right.DependsOn[index] {
			return false
		}
	}
	return true
}

func latestAttempt(step *logicalStep) *scannerrelease.BuildStep {
	if step == nil || len(step.Attempts) == 0 {
		return nil
	}
	return &step.Attempts[len(step.Attempts)-1]
}
