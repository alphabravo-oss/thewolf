package scannercustombuildworker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/alphabravocompany/thewolf/internal/scannerbuild"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

type Store interface {
	scannerrelease.CustomBuildRepository
}

type Executor interface {
	Build(context.Context, scannerbuild.BuildRequest, func(string)) (scannerbuild.BuildResult, error)
}

type ExecutorFunc func(context.Context, scannerbuild.BuildRequest, func(string)) (scannerbuild.BuildResult, error)

func (function ExecutorFunc) Build(
	ctx context.Context,
	request scannerbuild.BuildRequest,
	onLine func(string),
) (scannerbuild.BuildResult, error) {
	return function(ctx, request, onLine)
}

type CredentialResolver interface {
	Resolve(context.Context, string, string) (string, string, error)
}

type CredentialResolverFunc func(context.Context, string, string) (string, string, error)

func (function CredentialResolverFunc) Resolve(
	ctx context.Context,
	reference, userID string,
) (string, string, error) {
	return function(ctx, reference, userID)
}

type Config struct {
	Store             Store
	Executor          Executor
	Credentials       CredentialResolver
	WorkerID          string
	PollInterval      time.Duration
	HeartbeatInterval time.Duration
	LeaseDuration     time.Duration
	OperationTimeout  time.Duration
	Once              bool
}

type Worker struct {
	config Config
	now    func() time.Time
}

func New(config Config) (*Worker, error) {
	if config.Store == nil || config.Executor == nil ||
		strings.TrimSpace(config.WorkerID) == "" {
		return nil, errors.New("custom build worker requires store, executor, and worker ID")
	}
	if config.PollInterval <= 0 {
		config.PollInterval = 2 * time.Second
	}
	if config.HeartbeatInterval <= 0 {
		config.HeartbeatInterval = 10 * time.Second
	}
	if config.LeaseDuration <= 2*config.HeartbeatInterval {
		return nil, errors.New("custom build lease duration must exceed twice the heartbeat interval")
	}
	if config.OperationTimeout <= 0 {
		config.OperationTimeout = 2 * time.Hour
	}
	return &Worker{config: config, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (worker *Worker) Run(ctx context.Context) error {
	for {
		worked, err := worker.Once(ctx)
		if err != nil {
			return err
		}
		if worker.config.Once {
			return nil
		}
		if worked {
			continue
		}
		timer := time.NewTimer(worker.config.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (worker *Worker) Once(ctx context.Context) (bool, error) {
	now := worker.now()
	if _, err := worker.config.Store.ReclaimStaleCustomBuilds(ctx, now); err != nil {
		return false, err
	}
	build, err := worker.config.Store.ClaimNextCustomBuild(
		ctx, worker.config.WorkerID, now, now.Add(worker.config.LeaseDuration),
	)
	if err != nil || build == nil {
		return false, err
	}
	return true, worker.execute(ctx, build)
}

func (worker *Worker) execute(
	parent context.Context,
	build *scannerrelease.CustomBuild,
) error {
	started := worker.now()
	current, err := worker.config.Store.StartCustomBuild(
		parent, build.ID, build.LeaseToken, started,
	)
	if err != nil {
		return err
	}
	executionContext, cancel := context.WithTimeout(parent, worker.config.OperationTimeout)
	defer cancel()
	heartbeatDone := make(chan error, 1)
	go worker.heartbeat(executionContext, cancel, current, heartbeatDone)
	stopHeartbeat := func() error {
		cancel()
		heartbeatErr := <-heartbeatDone
		if heartbeatErr != nil && !errors.Is(heartbeatErr, context.Canceled) {
			return heartbeatErr
		}
		return nil
	}
	inventory, err := worker.config.Store.GetCustomBuild(parent, current.ID)
	if err != nil {
		_ = stopHeartbeat()
		return err
	}

	var requestedVariants []string
	var platforms []string
	if err := json.Unmarshal(
		[]byte(current.VariantsJSON), &requestedVariants,
	); err != nil {
		cancel()
		<-heartbeatDone
		return err
	}
	variants := make([]string, 0, len(requestedVariants))
	stateByVariant := make(map[string]scannerrelease.CustomBuildVariantState)
	for _, variant := range inventory.Variants {
		stateByVariant[variant.Variant] = variant.State
	}
	for _, variant := range requestedVariants {
		if stateByVariant[variant] == scannerrelease.CustomBuildVariantQueued {
			variants = append(variants, variant)
		}
	}
	if err := json.Unmarshal([]byte(current.PlatformsJSON), &platforms); err != nil {
		cancel()
		<-heartbeatDone
		return err
	}
	username, credential := "", ""
	if current.Push {
		if worker.config.Credentials == nil {
			cancel()
			<-heartbeatDone
			return errors.New("custom build credential resolver is not configured")
		}
		username, credential, err = worker.config.Credentials.Resolve(
			executionContext, current.SecretReference, current.UserID,
		)
		if err != nil {
			cancel()
			<-heartbeatDone
			return worker.failWithoutSecretDetail(parent, current, "credential_unavailable")
		}
	}
	for _, variant := range variants {
		select {
		case <-executionContext.Done():
			cancel()
			<-heartbeatDone
			if parent.Err() != nil {
				return parent.Err()
			}
			return worker.finishInterrupted(parent, current)
		default:
		}
		if _, err := worker.config.Store.StartCustomBuildVariant(
			executionContext, current.ID, variant, current.LeaseToken,
			worker.now(),
		); err != nil {
			cancel()
			<-heartbeatDone
			return err
		}
		var logMutex sync.Mutex
		logExhausted := false
		var logPersistenceError error
		buildContext, cancelBuild := context.WithCancel(executionContext)
		onLine := func(line string) {
			logMutex.Lock()
			defer logMutex.Unlock()
			if logExhausted {
				return
			}
			safe, redacted := redactBuildLine(line, credential)
			if _, appendErr := worker.config.Store.AppendCustomBuildLog(
				buildContext, current.ID, variant, current.LeaseToken,
				safe, redacted, worker.now(),
			); appendErr != nil {
				if errors.Is(
					appendErr, scannerrelease.ErrCustomBuildLogBudget,
				) {
					logExhausted = true
					_, markerErr := worker.config.Store.AppendCustomBuildLog(
						buildContext, current.ID, variant,
						current.LeaseToken,
						"[build log truncated: durable log budget exhausted]",
						true, worker.now(),
					)
					if markerErr != nil && !errors.Is(
						markerErr, scannerrelease.ErrCustomBuildLogBudget,
					) {
						logPersistenceError = markerErr
						cancelBuild()
					}
					return
				}
				logPersistenceError = appendErr
				cancelBuild()
			}
		}
		result, buildErr := worker.config.Executor.Build(
			buildContext,
			scannerbuild.BuildRequest{
				Variant: variant, Namespace: current.Namespace,
				Version: current.ReservedVersion, Push: current.Push,
				DockerHubUser: username, DockerHubPAT: credential,
				Platforms: strings.Join(platforms, ","),
			},
			onLine,
		)
		cancelBuild()
		logMutex.Lock()
		persistenceErr := logPersistenceError
		logMutex.Unlock()
		if persistenceErr != nil {
			cancel()
			<-heartbeatDone
			return persistenceErr
		}
		variantResult := scannerrelease.CustomBuildVariantResult{
			Refs: result.Refs, Digest: result.Digest,
			LoadedLocally: result.LoadedLocally, Pushed: current.Push,
		}
		if buildErr != nil {
			if executionContext.Err() != nil {
				cancel()
				<-heartbeatDone
				if parent.Err() != nil {
					return parent.Err()
				}
				return worker.finishInterrupted(parent, current)
			}
			variantResult.ErrorClass = "build_failed"
			variantResult.ErrorDetail = "scanner image build failed"
		}
		if _, err := worker.config.Store.CompleteCustomBuildVariant(
			executionContext, current.ID, variant, current.LeaseToken,
			variantResult, worker.now(),
		); err != nil {
			cancel()
			<-heartbeatDone
			return err
		}
	}
	if err := stopHeartbeat(); err != nil {
		return err
	}
	_, err = worker.config.Store.FinalizeCustomBuild(
		context.WithoutCancel(parent), current.ID, current.LeaseToken,
		worker.now(),
	)
	return err
}

func (worker *Worker) finishInterrupted(
	parent context.Context,
	build *scannerrelease.CustomBuild,
) error {
	ctx := context.WithoutCancel(parent)
	inventory, err := worker.config.Store.GetCustomBuild(ctx, build.ID)
	if err != nil {
		return err
	}
	if inventory.Build.CancelRequestedAt == nil {
		for _, variant := range inventory.Variants {
			switch variant.State {
			case scannerrelease.CustomBuildVariantCompleted,
				scannerrelease.CustomBuildVariantFailed,
				scannerrelease.CustomBuildVariantCancelled:
				continue
			case scannerrelease.CustomBuildVariantQueued:
				if _, err := worker.config.Store.StartCustomBuildVariant(
					ctx, build.ID, variant.Variant, build.LeaseToken,
					worker.now(),
				); err != nil {
					return err
				}
			}
			if _, err := worker.config.Store.CompleteCustomBuildVariant(
				ctx, build.ID, variant.Variant, build.LeaseToken,
				scannerrelease.CustomBuildVariantResult{
					ErrorClass:  "timeout",
					ErrorDetail: "scanner image build timed out",
				},
				worker.now(),
			); err != nil {
				return err
			}
		}
	}
	_, err = worker.config.Store.FinalizeCustomBuild(
		ctx, build.ID, build.LeaseToken, worker.now(),
	)
	return err
}

func (worker *Worker) heartbeat(
	ctx context.Context,
	cancel context.CancelFunc,
	build *scannerrelease.CustomBuild,
	done chan<- error,
) {
	ticker := time.NewTicker(worker.config.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			done <- ctx.Err()
			return
		case <-ticker.C:
			now := worker.now()
			status, err := worker.config.Store.HeartbeatCustomBuild(
				ctx, build.ID, build.LeaseToken, now,
				now.Add(worker.config.LeaseDuration),
			)
			if err != nil {
				cancel()
				done <- err
				return
			}
			if !status.Current || status.CancelRequested {
				cancel()
				done <- nil
				return
			}
		}
	}
}

func (worker *Worker) failWithoutSecretDetail(
	ctx context.Context,
	build *scannerrelease.CustomBuild,
	errorClass string,
) error {
	inventory, err := worker.config.Store.GetCustomBuild(ctx, build.ID)
	if err != nil {
		return err
	}
	for _, variant := range inventory.Variants {
		if variant.State != scannerrelease.CustomBuildVariantQueued {
			continue
		}
		if _, err := worker.config.Store.StartCustomBuildVariant(
			ctx, build.ID, variant.Variant, build.LeaseToken, worker.now(),
		); err != nil {
			return err
		}
		if _, err := worker.config.Store.CompleteCustomBuildVariant(
			ctx, build.ID, variant.Variant, build.LeaseToken,
			scannerrelease.CustomBuildVariantResult{
				ErrorClass:  errorClass,
				ErrorDetail: "registry credential could not be resolved",
			}, worker.now(),
		); err != nil {
			return err
		}
	}
	_, err = worker.config.Store.FinalizeCustomBuild(
		ctx, build.ID, build.LeaseToken, worker.now(),
	)
	return err
}

var secretAssignmentPattern = regexp.MustCompile(
	`(?i)(password|token|pat|secret)(\s*[:=]\s*)([^\s,;]+)`,
)

func redactBuildLine(line, credential string) (string, bool) {
	redacted := false
	valid := strings.ToValidUTF8(line, "�")
	if valid != line {
		line = valid
		redacted = true
	}
	sanitized := strings.Map(func(character rune) rune {
		if character == '\t' {
			return character
		}
		if unicode.IsControl(character) {
			redacted = true
			return ' '
		}
		return character
	}, line)
	line = sanitized
	for _, candidate := range credentialEncodings(credential) {
		if candidate != "" && strings.Contains(line, candidate) {
			line = strings.ReplaceAll(line, candidate, "[REDACTED]")
			redacted = true
		}
	}
	replaced := secretAssignmentPattern.ReplaceAllString(line, "$1$2[REDACTED]")
	if replaced != line {
		line = replaced
		redacted = true
	}
	if len(line) > customBuildMaxLogLine {
		suffix := "…[truncated]"
		limit := customBuildMaxLogLine - len(suffix)
		for limit > 0 && !utf8.ValidString(line[:limit]) {
			limit--
		}
		line = line[:limit] + suffix
		redacted = true
	}
	return line, redacted
}

func credentialEncodings(credential string) []string {
	if credential == "" {
		return nil
	}
	return []string{
		credential,
		base64.StdEncoding.EncodeToString([]byte(credential)),
		base64.RawStdEncoding.EncodeToString([]byte(credential)),
		base64.URLEncoding.EncodeToString([]byte(credential)),
		base64.RawURLEncoding.EncodeToString([]byte(credential)),
	}
}

const customBuildMaxLogLine = scannerrelease.CustomBuildMaxLogLineBytes
