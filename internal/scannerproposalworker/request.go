package scannerproposalworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

var ErrNoCandidateUpdates = errors.New("scanner candidate inputs are already current")

type candidateSelection struct {
	Mode            string          `json:"mode"`
	DiscoveryRunID  string          `json:"discovery_run_id,omitempty"`
	Items           []string        `json:"items"`
	NoOpIfUnchanged bool            `json:"no_op_if_unchanged,omitempty"`
	ForceRebuild    bool            `json:"force_rebuild,omitempty"`
	RebuildReason   string          `json:"rebuild_reason,omitempty"`
	LegacyScope     json.RawMessage `json:"scope,omitempty"`
}

func proposalRequest(
	ctx context.Context,
	store scannerrelease.Persistence,
	candidate *scannerrelease.Candidate,
) (Request, error) {
	if store == nil || candidate == nil {
		return Request{}, errors.New("scanner proposal persistence and candidate are required")
	}
	selection, err := decodeCandidateSelection(candidate.SelectionJSON)
	if err != nil {
		return Request{}, err
	}
	if selection.DiscoveryRunID != "" && selection.DiscoveryRunID != candidate.DiscoveryRunID {
		return Request{}, errors.New("scanner candidate selection discovery does not match its immutable binding")
	}
	risk := json.RawMessage(candidate.RiskSummaryJSON)
	if err := validateJSONObject(risk, "candidate risk summary"); err != nil {
		return Request{}, err
	}
	var gates []string
	if err := decodeStrictJSON([]byte(candidate.RequiredGatesJSON), &gates); err != nil {
		return Request{}, fmt.Errorf("decode candidate required gates: %w", err)
	}
	if len(gates) == 0 {
		return Request{}, errors.New("scanner candidate has no immutable required gates")
	}
	updates, err := selectedUpdates(ctx, store, candidate, selection)
	if err != nil {
		return Request{}, err
	}
	epoch := int64(0)
	if !candidate.CreatedAt.IsZero() {
		epoch = candidate.CreatedAt.UTC().Unix()
	}
	return Request{
		CandidateID: candidate.ID, DefinitionCommit: candidate.DefinitionCommit,
		Selection: json.RawMessage(candidate.SelectionJSON), Updates: updates,
		RiskSummary: risk, RequiredGates: append([]string(nil), gates...),
		SourceDateEpoch: epoch, ExpectedHead: candidate.ProposedCommit,
		PolicyID: candidate.PolicyID, PolicyRevision: candidate.PolicyRevision,
		IdempotencyKey: candidate.IdempotencyKey + "/proposal",
	}, nil
}

func decodeCandidateSelection(raw string) (candidateSelection, error) {
	var selection candidateSelection
	if err := decodeStrictJSON([]byte(raw), &selection); err != nil {
		return candidateSelection{}, fmt.Errorf("decode scanner candidate selection: %w", err)
	}
	if selection.Mode == "" {
		selection.Mode = "explicit"
	}
	switch selection.Mode {
	case "complete":
		if len(selection.Items) != 0 {
			return candidateSelection{}, errors.New("complete scanner candidate selection must not list items")
		}
		if selection.NoOpIfUnchanged && selection.ForceRebuild {
			return candidateSelection{}, errors.New("scanner candidate cannot both force a rebuild and no-op when unchanged")
		}
		if selection.ForceRebuild && strings.TrimSpace(selection.RebuildReason) == "" {
			return candidateSelection{}, errors.New("forced scanner candidate rebuild requires a reason")
		}
	case "discovery_run":
		// Normalize candidates produced by the pre-canonical scheduler format.
		// The aggregate's DiscoveryRunID remains the authoritative binding.
		if selection.DiscoveryRunID == "" || len(selection.Items) != 0 {
			return candidateSelection{}, errors.New("discovery-run scanner candidate selection requires one run and no items")
		}
		selection.Mode = "complete"
	case "explicit":
		if len(selection.Items) == 0 {
			return candidateSelection{}, errors.New("explicit scanner candidate selection requires items")
		}
	default:
		return candidateSelection{}, fmt.Errorf("unsupported scanner candidate selection mode %q", selection.Mode)
	}
	return selection, nil
}

func selectedUpdates(
	ctx context.Context,
	store scannerrelease.Persistence,
	candidate *scannerrelease.Candidate,
	selection candidateSelection,
) ([]SelectedUpdate, error) {
	if candidate.DiscoveryRunID == "" {
		return nil, errors.New("scanner proposal candidate is not bound to a discovery run")
	}
	items, err := store.ListUpdateItems(ctx, candidate.DiscoveryRunID)
	if err != nil {
		return nil, fmt.Errorf("load scanner proposal discovery items: %w", err)
	}
	bySelectionKey := make(map[string]scannerrelease.UpdateItem, len(items)*2)
	for _, item := range items {
		bySelectionKey[item.ID] = item
		bySelectionKey[string(item.ComponentType)+":"+item.ComponentName] = item
	}
	selected := make(map[string]scannerrelease.UpdateItem)
	if selection.Mode == "complete" {
		for _, item := range items {
			if item.Status == "update_available" {
				selected[item.ID] = item
			}
		}
	} else {
		for _, key := range selection.Items {
			item, exists := bySelectionKey[key]
			if !exists {
				return nil, fmt.Errorf("selected scanner update %q is not part of discovery run %s", key, candidate.DiscoveryRunID)
			}
			selected[item.ID] = item
		}
	}
	if len(selected) == 0 {
		if selection.Mode == "complete" {
			if selection.NoOpIfUnchanged {
				return nil, ErrNoCandidateUpdates
			}
			// An on-demand complete rebuild intentionally proposes the same
			// definition even when discovery found no version transition.
			return []SelectedUpdate{}, nil
		}
		return nil, errors.New("scanner proposal selection contains no available updates")
	}

	// Base image variants sharing one transition are one compatibility group.
	// Selecting one expands the exact same current/new digest transition so a
	// proposal cannot leave sibling scanner images on a stale base silently.
	for _, chosen := range selected {
		if chosen.ComponentType != scannerrelease.ComponentBaseImage {
			continue
		}
		for _, item := range items {
			if item.ComponentType == scannerrelease.ComponentBaseImage &&
				item.CurrentValue == chosen.CurrentValue &&
				item.AvailableDigest == chosen.AvailableDigest &&
				item.Status == "update_available" {
				selected[item.ID] = item
			}
		}
	}
	// Rust's release metadata is represented both by the toolchain source and
	// by the clippy tool pin. They must move together to keep the lock and
	// installed cargo-clippy version consistent.
	for _, chosen := range selected {
		if !((chosen.ComponentType == scannerrelease.ComponentToolchain && chosen.ComponentName == "rust") ||
			(chosen.ComponentType == scannerrelease.ComponentTool && chosen.ComponentName == "clippy")) {
			continue
		}
		for _, item := range items {
			paired := (item.ComponentType == scannerrelease.ComponentToolchain && item.ComponentName == "rust") ||
				(item.ComponentType == scannerrelease.ComponentTool && item.ComponentName == "clippy")
			if paired && item.AvailableValue == chosen.AvailableValue && item.Status == "update_available" {
				selected[item.ID] = item
			}
		}
	}

	updates := make([]SelectedUpdate, 0, len(selected))
	for _, item := range selected {
		if err := validateSelectedItem(item); err != nil {
			return nil, err
		}
		updates = append(updates, SelectedUpdate{
			ID: item.ID, ComponentType: string(item.ComponentType),
			ComponentName: item.ComponentName, CurrentValue: item.CurrentValue,
			AvailableValue: item.AvailableValue, AvailableDigest: item.AvailableDigest,
			RiskClass:     string(item.RiskClass),
			Evidence:      json.RawMessage(item.SourceEvidenceJSON),
			Compatibility: json.RawMessage(item.CompatibilityJSON),
		})
	}
	sort.Slice(updates, func(i, j int) bool {
		if updates[i].ComponentType == updates[j].ComponentType {
			return updates[i].ComponentName < updates[j].ComponentName
		}
		return updates[i].ComponentType < updates[j].ComponentType
	})
	return updates, nil
}

func validateSelectedItem(item scannerrelease.UpdateItem) error {
	switch item.ComponentType {
	case scannerrelease.ComponentTool, scannerrelease.ComponentUpstreamImage,
		scannerrelease.ComponentBaseImage, scannerrelease.ComponentToolchain:
	default:
		return fmt.Errorf("scanner update %q has unsupported component type %q", item.ID, item.ComponentType)
	}
	if item.Status != "update_available" || item.SelectionState == "held" ||
		item.SelectionState == "unsupported" || item.SelectionState == "unreachable" {
		return fmt.Errorf("scanner update %q is not selectable in state %q/%q", item.ID, item.Status, item.SelectionState)
	}
	if strings.TrimSpace(item.ComponentName) == "" || strings.TrimSpace(item.AvailableValue) == "" {
		return fmt.Errorf("scanner update %q has incomplete resolved identity", item.ID)
	}
	if err := validateJSONObject(json.RawMessage(item.SourceEvidenceJSON), "scanner update evidence"); err != nil {
		return fmt.Errorf("scanner update %q: %w", item.ID, err)
	}
	if err := validateJSONObject(json.RawMessage(item.CompatibilityJSON), "scanner update compatibility"); err != nil {
		return fmt.Errorf("scanner update %q: %w", item.ID, err)
	}
	return nil
}

func validateJSONObject(raw json.RawMessage, label string) error {
	if len(raw) == 0 {
		return fmt.Errorf("%s is required", label)
	}
	var value map[string]any
	if err := decodeStrictJSON(raw, &value); err != nil {
		return fmt.Errorf("%s must be one JSON object: %w", label, err)
	}
	return nil
}

func decodeStrictJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return ensureEOF(decoder)
}
