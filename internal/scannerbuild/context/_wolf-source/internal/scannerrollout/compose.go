package scannerrollout

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerdiscovery"
)

type ComposeActionRunner interface {
	RunComposeAction(context.Context, string, DeploymentAssignment) (DeploymentObservation, error)
}

// ComposeControl persists desired and observed cohort state independently.
// The reload adapter must return deployment-observed identity; Wolf does not
// infer success merely because the reload command exited zero.
type ComposeControl struct {
	StateRoot string
	Runner    ComposeActionRunner
}

func (c ComposeControl) Apply(
	ctx context.Context,
	assignment DeploymentAssignment,
) error {
	if err := c.validate(); err != nil {
		return err
	}
	desiredPath, observedPath, controlPath, err := c.paths(assignment.CohortID)
	if err != nil {
		return err
	}
	if current, err := readDeploymentObservation(observedPath); err == nil &&
		observationMatches(assignment, current) {
		return nil
	}
	previous, previousErr := os.ReadFile(desiredPath)
	previousControl, previousControlErr := os.ReadFile(controlPath)
	raw, err := encodeDeploymentAssignment(assignment)
	if err != nil {
		return err
	}
	if err := atomicDeploymentWrite(desiredPath, raw); err != nil {
		return err
	}
	if err := atomicDeploymentWrite(controlPath, []byte("resumed\n")); err != nil {
		restoreDeploymentFile(desiredPath, previous, previousErr)
		return err
	}
	observation, err := c.Runner.RunComposeAction(ctx, "apply", assignment)
	if err != nil {
		restoreDeploymentFile(desiredPath, previous, previousErr)
		restoreDeploymentFile(controlPath, previousControl, previousControlErr)
		return err
	}
	if !observationMatches(assignment, observation) {
		restoreDeploymentFile(desiredPath, previous, previousErr)
		restoreDeploymentFile(controlPath, previousControl, previousControlErr)
		return errors.New("Compose reload observation does not match the desired assignment")
	}
	observed, err := json.Marshal(observation)
	if err != nil {
		restoreDeploymentFile(desiredPath, previous, previousErr)
		return err
	}
	if err := atomicDeploymentWrite(observedPath, observed); err != nil {
		restoreDeploymentFile(desiredPath, previous, previousErr)
		restoreDeploymentFile(controlPath, previousControl, previousControlErr)
		return err
	}
	return nil
}

func (c ComposeControl) Observe(
	_ context.Context,
	assignment DeploymentAssignment,
) (DeploymentObservation, error) {
	if err := c.validate(); err != nil {
		return DeploymentObservation{}, err
	}
	_, observedPath, controlPath, err := c.paths(assignment.CohortID)
	if err != nil {
		return DeploymentObservation{}, err
	}
	if control, readErr := os.ReadFile(controlPath); readErr == nil &&
		strings.TrimSpace(string(control)) != "resumed" {
		return DeploymentObservation{}, fmt.Errorf(
			"Compose cohort is %s", strings.TrimSpace(string(control)),
		)
	}
	return readDeploymentObservation(observedPath)
}

func (c ComposeControl) Pause(
	ctx context.Context,
	assignment DeploymentAssignment,
) error {
	return c.setLifecycle(ctx, "paused", assignment)
}

func (c ComposeControl) Resume(
	ctx context.Context,
	assignment DeploymentAssignment,
) error {
	return c.setLifecycle(ctx, "resumed", assignment)
}

func (c ComposeControl) Cancel(
	ctx context.Context,
	assignment DeploymentAssignment,
) error {
	return c.setLifecycle(ctx, "cancelled", assignment)
}

func (c ComposeControl) setLifecycle(
	ctx context.Context,
	action string,
	assignment DeploymentAssignment,
) error {
	if err := c.validate(); err != nil {
		return err
	}
	_, _, controlPath, err := c.paths(assignment.CohortID)
	if err != nil {
		return err
	}
	if current, readErr := os.ReadFile(controlPath); readErr == nil &&
		strings.TrimSpace(string(current)) == action {
		return nil
	}
	if _, err := c.Runner.RunComposeAction(ctx, action, assignment); err != nil {
		return err
	}
	return atomicDeploymentWrite(controlPath, []byte(action+"\n"))
}

func (c ComposeControl) validate() error {
	if !filepath.IsAbs(c.StateRoot) || c.Runner == nil {
		return errors.New("Compose control requires an absolute state root and reload runner")
	}
	return os.MkdirAll(c.StateRoot, 0o750)
}

func (c ComposeControl) paths(cohortID string) (string, string, string, error) {
	if strings.TrimSpace(cohortID) == "" {
		return "", "", "", errors.New("Compose cohort ID is required")
	}
	name := strings.TrimPrefix(digestSynthetic([]byte(cohortID)), "sha256:")[:24]
	return filepath.Join(c.StateRoot, name+".desired.json"),
		filepath.Join(c.StateRoot, name+".observed.json"),
		filepath.Join(c.StateRoot, name+".control"), nil
}

type CommandComposeRunner struct {
	Path           string
	Args           []string
	Environment    []string
	MaxOutputBytes int64
}

func (r CommandComposeRunner) RunComposeAction(
	ctx context.Context,
	action string,
	assignment DeploymentAssignment,
) (DeploymentObservation, error) {
	if strings.TrimSpace(r.Path) == "" {
		return DeploymentObservation{}, errors.New("Compose reload adapter path is required")
	}
	raw, err := json.Marshal(struct {
		Action     string               `json:"action"`
		Assignment DeploymentAssignment `json:"assignment"`
	}{Action: action, Assignment: assignment})
	if err != nil {
		return DeploymentObservation{}, err
	}
	command := exec.CommandContext(ctx, r.Path, r.Args...)
	if r.Environment != nil {
		command.Env = append([]string(nil), r.Environment...)
	}
	command.Stdin = bytes.NewReader(raw)
	limit := r.MaxOutputBytes
	if limit <= 0 {
		limit = 1 << 20
	}
	var stdout, stderr bytes.Buffer
	stdoutWriter := &limitedSyntheticWriter{writer: &stdout, remaining: limit}
	stderrWriter := &limitedSyntheticWriter{writer: &stderr, remaining: limit}
	command.Stdout = stdoutWriter
	command.Stderr = stderrWriter
	err = command.Run()
	if ctx.Err() != nil {
		return DeploymentObservation{}, ctx.Err()
	}
	if err != nil {
		return DeploymentObservation{}, fmt.Errorf(
			"Compose reload adapter failed: %w: %s",
			err, scannerdiscovery.RedactText(strings.TrimSpace(stderr.String())),
		)
	}
	if stdoutWriter.Overflowed() || stderrWriter.Overflowed() {
		return DeploymentObservation{}, errors.New("Compose reload adapter output exceeds limit")
	}
	if action != "apply" {
		return DeploymentObservation{}, nil
	}
	var observation DeploymentObservation
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&observation); err != nil {
		return DeploymentObservation{}, fmt.Errorf("decode Compose reload observation: %w", err)
	}
	if err := ensureSyntheticEOF(decoder); err != nil {
		return DeploymentObservation{}, fmt.Errorf("decode Compose reload observation: %w", err)
	}
	return observation, nil
}

type DockerImageCache struct {
	Path        string
	Environment []string
	Now         func() time.Time
}

func (c DockerImageCache) Prepare(
	ctx context.Context,
	_ string,
	plan DeploymentPlan,
) (CacheVerification, error) {
	path := strings.TrimSpace(c.Path)
	if path == "" {
		path = "docker"
	}
	verified := make(map[string]string, len(plan.ImageDigests))
	for _, key := range sortedDeploymentKeys(plan.ImageReferences) {
		reference := plan.ImageReferences[key]
		if err := runCacheCommand(ctx, path, c.Environment, "pull", reference); err != nil {
			return CacheVerification{}, err
		}
		output, err := outputCacheCommand(
			ctx, path, c.Environment,
			"image", "inspect", "--format={{json .RepoDigests}}", reference,
		)
		if err != nil {
			return CacheVerification{}, err
		}
		if !strings.Contains(output, "@"+plan.ImageDigests[key]) {
			return CacheVerification{}, fmt.Errorf(
				"Docker cache readback for %q did not contain %s",
				key, plan.ImageDigests[key],
			)
		}
		verified[key] = plan.ImageDigests[key]
	}
	now := time.Now().UTC()
	if c.Now != nil {
		now = c.Now().UTC()
	}
	return CacheVerification{Digests: verified, VerifiedAt: now}, nil
}

func runCacheCommand(
	ctx context.Context,
	path string,
	environment []string,
	args ...string,
) error {
	_, err := outputCacheCommand(ctx, path, environment, args...)
	return err
}

func outputCacheCommand(
	ctx context.Context,
	path string,
	environment []string,
	args ...string,
) (string, error) {
	command := exec.CommandContext(ctx, path, args...)
	if environment != nil {
		command.Env = append([]string(nil), environment...)
	}
	var output bytes.Buffer
	writer := &limitedSyntheticWriter{writer: &output, remaining: 1 << 20}
	command.Stdout = writer
	command.Stderr = writer
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("%s %s failed: %w: %s",
			path, strings.Join(args, " "), err,
			scannerdiscovery.RedactText(strings.TrimSpace(output.String())))
	}
	if writer.Overflowed() {
		return "", fmt.Errorf("%s output exceeds limit", path)
	}
	return output.String(), nil
}

func readDeploymentObservation(path string) (DeploymentObservation, error) {
	file, err := os.Open(path)
	if err != nil {
		return DeploymentObservation{}, err
	}
	defer file.Close()
	var result DeploymentObservation
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return DeploymentObservation{}, err
	}
	return result, nil
}

func atomicDeploymentWrite(path string, value []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(value); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func restoreDeploymentFile(path string, value []byte, readErr error) {
	if readErr == nil {
		_ = atomicDeploymentWrite(path, value)
		return
	}
	if errors.Is(readErr, os.ErrNotExist) {
		_ = os.Remove(path)
	}
}
