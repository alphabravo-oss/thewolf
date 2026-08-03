// Package scannerobservability provides bounded-cardinality metrics and
// component health for the scanner release control plane.
package scannerobservability

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

type Component string

const (
	ComponentScheduler    Component = "scheduler"
	ComponentAlert        Component = "alert"
	ComponentDiscovery    Component = "discovery"
	ComponentProposal     Component = "proposal"
	ComponentBuild        Component = "build"
	ComponentRollout      Component = "rollout"
	ComponentNotification Component = "notification"
	ComponentRegistry     Component = "registry"
	ComponentFixed        Component = "fixed"
	ComponentQuality      Component = "quality"
	ComponentIntegration  Component = "integration"
)

var allComponents = []Component{
	ComponentAlert,
	ComponentBuild,
	ComponentDiscovery,
	ComponentFixed,
	ComponentIntegration,
	ComponentNotification,
	ComponentProposal,
	ComponentQuality,
	ComponentRegistry,
	ComponentRollout,
	ComponentScheduler,
}

var componentStates = []string{
	"disabled",
	"starting",
	"idle",
	"active",
	"busy",
	"degraded",
	"stopped",
}

type Observer interface {
	ObserveRun(Component, string, time.Duration)
	ObserveClaim(Component, string)
	ObserveLease(Component, string)
	ObserveRetry(Component, string)
	ObserveResult(Component, string)
	SetState(Component, string)
	SetStuckWork(Component, string, int)
	SetQueueDepth(Component, string, int)
}

type DatabaseCheck func(context.Context) error
type MaintenanceCheck func(context.Context) (bool, error)

type Registry struct {
	mu         sync.RWMutex
	startedAt  time.Time
	now        func() time.Time
	checkDB    DatabaseCheck
	checkMaint MaintenanceCheck
	components map[Component]*componentStatus
	counters   map[metricKey]float64
	gauges     map[metricKey]float64
}

type componentStatus struct {
	Enabled      bool
	State        string
	LastActivity time.Time
	LastSuccess  time.Time
	Stuck        map[string]int
	QueueDepth   map[string]int
	RunCounts    map[string]int64
	ResultCounts map[string]int64
	RunDuration  time.Duration
}

type metricKey struct {
	Name   string
	Labels string
}

type ComponentStatus struct {
	Component            Component        `json:"component"`
	Enabled              bool             `json:"enabled"`
	Status               string           `json:"status"`
	Ready                bool             `json:"ready"`
	LastActivity         *time.Time       `json:"last_activity,omitempty"`
	LastSuccess          *time.Time       `json:"last_success,omitempty"`
	StuckWork            map[string]int   `json:"stuck_work,omitempty"`
	QueueDepth           map[string]int   `json:"queue_depth,omitempty"`
	RunCounts            map[string]int64 `json:"run_counts,omitempty"`
	ResultCounts         map[string]int64 `json:"result_counts,omitempty"`
	AverageRunDurationMS int64            `json:"average_run_duration_ms,omitempty"`
}

type HealthSnapshot struct {
	Status      string            `json:"status"`
	Ready       bool              `json:"ready"`
	Database    string            `json:"database"`
	Maintenance string            `json:"maintenance"`
	UptimeMS    int64             `json:"uptime_ms"`
	Components  []ComponentStatus `json:"components"`
}

var Default = NewRegistry()

func NewRegistry() *Registry {
	now := time.Now
	registry := &Registry{
		startedAt:  now().UTC(),
		now:        now,
		components: make(map[Component]*componentStatus, len(allComponents)),
		counters:   make(map[metricKey]float64),
		gauges:     make(map[metricKey]float64),
	}
	for _, component := range allComponents {
		registry.components[component] = &componentStatus{
			State:        "disabled",
			Stuck:        make(map[string]int),
			QueueDepth:   make(map[string]int),
			RunCounts:    make(map[string]int64),
			ResultCounts: make(map[string]int64),
		}
		for _, state := range componentStates {
			value := float64(0)
			if state == "disabled" {
				value = 1
			}
			registry.gauges[metricKey{
				Name:   "wolf_scanner_release_component_state",
				Labels: labelsFor("component", string(component), "state", state),
			}] = value
		}
	}
	registry.components[Component("unknown")] = &componentStatus{
		State:        "disabled",
		Stuck:        make(map[string]int),
		QueueDepth:   make(map[string]int),
		RunCounts:    make(map[string]int64),
		ResultCounts: make(map[string]int64),
	}
	return registry
}

func (r *Registry) SetDatabaseCheck(check DatabaseCheck) {
	r.mu.Lock()
	r.checkDB = check
	r.mu.Unlock()
}

func (r *Registry) SetMaintenanceCheck(check MaintenanceCheck) {
	r.mu.Lock()
	r.checkMaint = check
	r.mu.Unlock()
}

func (r *Registry) Enable(component Component, enabled bool) {
	component = normalizeComponent(component)
	r.mu.Lock()
	status := r.components[component]
	status.Enabled = enabled
	if enabled {
		status.State = "starting"
	} else {
		status.State = "disabled"
		status.Stuck = make(map[string]int)
		status.QueueDepth = make(map[string]int)
	}
	status.LastActivity = r.now().UTC()
	r.mu.Unlock()
	if enabled {
		r.SetState(component, "starting")
	} else {
		r.SetState(component, "disabled")
	}
}

func (r *Registry) ObserveRun(component Component, result string, duration time.Duration) {
	component = normalizeComponent(component)
	result = normalizeResult(result)
	if duration < 0 {
		duration = 0
	}
	labels := labelsFor("component", string(component), "result", result)
	r.addCounter("wolf_scanner_release_component_runs_total", labels, 1)
	r.addCounter("wolf_scanner_release_component_run_duration_seconds_sum", labels, duration.Seconds())
	r.addCounter("wolf_scanner_release_component_run_duration_seconds_count", labels, 1)
	r.mu.Lock()
	status := r.components[component]
	status.RunCounts[result]++
	status.RunDuration += duration
	r.mu.Unlock()
	r.touch(component, result == "success")
}

func (r *Registry) ObserveClaim(component Component, result string) {
	component = normalizeComponent(component)
	result = normalizeClaimResult(result)
	r.addCounter(
		"wolf_scanner_release_claims_total",
		labelsFor("component", string(component), "result", result),
		1,
	)
	r.touch(component, result == "acquired")
}

func (r *Registry) ObserveLease(component Component, result string) {
	component = normalizeComponent(component)
	result = normalizeLeaseResult(result)
	r.addCounter(
		"wolf_scanner_release_lease_events_total",
		labelsFor("component", string(component), "result", result),
		1,
	)
	r.touch(component, result == "heartbeat" || result == "completed")
}

func (r *Registry) ObserveRetry(component Component, reason string) {
	component = normalizeComponent(component)
	reason = normalizeRetryReason(reason)
	r.addCounter(
		"wolf_scanner_release_retries_total",
		labelsFor("component", string(component), "reason", reason),
		1,
	)
	r.touch(component, false)
}

func (r *Registry) ObserveResult(component Component, state string) {
	component = normalizeComponent(component)
	state = normalizeWorkState(state)
	r.addCounter(
		"wolf_scanner_release_results_total",
		labelsFor("component", string(component), "state", state),
		1,
	)
	r.mu.Lock()
	r.components[component].ResultCounts[state]++
	r.mu.Unlock()
	r.touch(component, state == "completed" || state == "partial")
}

func (r *Registry) SetState(component Component, state string) {
	component = normalizeComponent(component)
	state = normalizeComponentState(state)
	r.mu.Lock()
	status := r.components[component]
	status.State = state
	status.LastActivity = r.now().UTC()
	for _, possible := range componentStates {
		value := float64(0)
		if possible == state {
			value = 1
		}
		r.gauges[metricKey{
			Name:   "wolf_scanner_release_component_state",
			Labels: labelsFor("component", string(component), "state", possible),
		}] = value
	}
	r.mu.Unlock()
}

func (r *Registry) SetStuckWork(component Component, kind string, count int) {
	component = normalizeComponent(component)
	kind = normalizeStuckKind(kind)
	if count < 0 {
		count = 0
	}
	r.mu.Lock()
	status := r.components[component]
	status.Stuck[kind] = count
	status.LastActivity = r.now().UTC()
	r.gauges[metricKey{
		Name:   "wolf_scanner_release_stuck_work",
		Labels: labelsFor("component", string(component), "kind", kind),
	}] = float64(count)
	r.mu.Unlock()
}

func (r *Registry) SetQueueDepth(component Component, state string, count int) {
	component = normalizeComponent(component)
	state = normalizeQueueState(state)
	if count < 0 {
		count = 0
	}
	r.mu.Lock()
	status := r.components[component]
	status.LastActivity = r.now().UTC()
	status.QueueDepth[state] = count
	r.gauges[metricKey{
		Name:   "wolf_scanner_release_queue_depth",
		Labels: labelsFor("component", string(component), "state", state),
	}] = float64(count)
	r.mu.Unlock()
}

// SetAlertCount publishes the current durable open-alert inventory. Severity
// is deliberately bounded to avoid policy- or finding-derived metric labels.
func (r *Registry) SetAlertCount(severity string, count int) {
	severity = normalize(severity, []string{"warning", "critical"}, "warning")
	if count < 0 {
		count = 0
	}
	r.mu.Lock()
	r.gauges[metricKey{
		Name:   "wolf_scanner_release_alerts",
		Labels: labelsFor("severity", severity),
	}] = float64(count)
	r.mu.Unlock()
}

func (r *Registry) Snapshot(ctx context.Context) HealthSnapshot {
	r.mu.RLock()
	checkDB := r.checkDB
	checkMaint := r.checkMaint
	startedAt := r.startedAt
	r.mu.RUnlock()

	database := "ok"
	if checkDB == nil {
		database = "unknown"
	} else if err := checkDB(ctx); err != nil {
		database = "unavailable"
	}
	maintenance := "normal"
	if checkMaint != nil {
		active, err := checkMaint(ctx)
		switch {
		case err != nil:
			maintenance = "unavailable"
		case active:
			maintenance = "restore"
		}
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	snapshot := HealthSnapshot{
		Status:      "disabled",
		Ready:       database != "unavailable" && maintenance == "normal",
		Database:    database,
		Maintenance: maintenance,
		UptimeMS:    r.now().UTC().Sub(startedAt).Milliseconds(),
	}
	enabled := 0
	for _, component := range allComponents {
		current := r.components[component]
		entry := ComponentStatus{
			Component: component,
			Enabled:   current.Enabled,
			Status:    current.State,
			Ready:     !current.Enabled,
		}
		if !current.LastActivity.IsZero() {
			value := current.LastActivity
			entry.LastActivity = &value
		}
		if !current.LastSuccess.IsZero() {
			value := current.LastSuccess
			entry.LastSuccess = &value
		}
		stuck := 0
		for kind, count := range current.Stuck {
			if count <= 0 {
				continue
			}
			if entry.StuckWork == nil {
				entry.StuckWork = make(map[string]int)
			}
			entry.StuckWork[kind] = count
			stuck += count
		}
		for state, count := range current.QueueDepth {
			if count < 0 {
				continue
			}
			if entry.QueueDepth == nil {
				entry.QueueDepth = make(map[string]int)
			}
			entry.QueueDepth[state] = count
		}
		runTotal := int64(0)
		for result, count := range current.RunCounts {
			if count <= 0 {
				continue
			}
			if entry.RunCounts == nil {
				entry.RunCounts = make(map[string]int64)
			}
			entry.RunCounts[result] = count
			runTotal += count
		}
		for state, count := range current.ResultCounts {
			if count <= 0 {
				continue
			}
			if entry.ResultCounts == nil {
				entry.ResultCounts = make(map[string]int64)
			}
			entry.ResultCounts[state] = count
		}
		if runTotal > 0 {
			entry.AverageRunDurationMS = current.RunDuration.Milliseconds() / runTotal
		}
		if current.Enabled {
			enabled++
			entry.Ready = database != "unavailable" &&
				maintenance == "normal" &&
				current.State != "starting" &&
				current.State != "degraded" &&
				current.State != "stopped" &&
				stuck == 0
			if stuck > 0 {
				entry.Status = "stale_or_stuck"
			}
			if !entry.Ready {
				snapshot.Ready = false
			}
		}
		snapshot.Components = append(snapshot.Components, entry)
	}
	switch {
	case database == "unavailable":
		snapshot.Status = "database_unavailable"
		snapshot.Ready = false
	case maintenance == "unavailable":
		snapshot.Status = "maintenance_unavailable"
		snapshot.Ready = false
	case maintenance == "restore":
		snapshot.Status = "restore_maintenance"
		snapshot.Ready = false
	case !snapshot.Ready:
		snapshot.Status = "degraded"
	case enabled > 0:
		snapshot.Status = "active"
	default:
		snapshot.Status = "disabled"
	}
	return snapshot
}

func (r *Registry) RenderPrometheus(ctx context.Context, output io.Writer) error {
	snapshot := r.Snapshot(ctx)
	r.mu.Lock()
	databaseReady := float64(0)
	if snapshot.Database == "ok" {
		databaseReady = 1
	}
	r.gauges[metricKey{Name: "wolf_scanner_release_database_ready"}] = databaseReady
	maintenanceActive := float64(0)
	if snapshot.Maintenance == "restore" {
		maintenanceActive = 1
	}
	r.gauges[metricKey{Name: "wolf_scanner_release_restore_maintenance_active"}] = maintenanceActive
	for _, component := range snapshot.Components {
		ready := float64(0)
		if component.Ready {
			ready = 1
		}
		r.gauges[metricKey{
			Name:   "wolf_scanner_release_component_ready",
			Labels: labelsFor("component", string(component.Component)),
		}] = ready
	}
	samples := make([]struct {
		key   metricKey
		value float64
	}, 0, len(r.counters)+len(r.gauges))
	for key, value := range r.counters {
		samples = append(samples, struct {
			key   metricKey
			value float64
		}{key, value})
	}
	for key, value := range r.gauges {
		samples = append(samples, struct {
			key   metricKey
			value float64
		}{key, value})
	}
	r.mu.Unlock()

	sort.Slice(samples, func(i, j int) bool {
		if samples[i].key.Name == samples[j].key.Name {
			return samples[i].key.Labels < samples[j].key.Labels
		}
		return samples[i].key.Name < samples[j].key.Name
	})
	lastName := ""
	for _, sample := range samples {
		if sample.key.Name != lastName {
			metricType := "counter"
			if strings.HasSuffix(sample.key.Name, "_ready") ||
				strings.HasSuffix(sample.key.Name, "_active") ||
				strings.HasSuffix(sample.key.Name, "_state") ||
				strings.HasSuffix(sample.key.Name, "_work") ||
				strings.HasSuffix(sample.key.Name, "_depth") ||
				strings.HasSuffix(sample.key.Name, "_alerts") {
				metricType = "gauge"
			}
			if _, err := fmt.Fprintf(output, "# TYPE %s %s\n", sample.key.Name, metricType); err != nil {
				return err
			}
			lastName = sample.key.Name
		}
		if _, err := fmt.Fprintf(
			output, "%s%s %g\n", sample.key.Name, sample.key.Labels, sample.value,
		); err != nil {
			return err
		}
	}
	return nil
}

func (r *Registry) MetricsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if err := r.RenderPrometheus(request.Context(), w); err != nil {
			http.Error(w, "failed to render metrics", http.StatusInternalServerError)
		}
	})
}

func (r *Registry) addCounter(name, labels string, value float64) {
	r.mu.Lock()
	r.counters[metricKey{Name: name, Labels: labels}] += value
	r.mu.Unlock()
}

func (r *Registry) touch(component Component, success bool) {
	r.mu.Lock()
	status := r.components[component]
	status.LastActivity = r.now().UTC()
	if success {
		status.LastSuccess = status.LastActivity
	}
	r.mu.Unlock()
}

func labelsFor(values ...string) string {
	if len(values) == 0 {
		return ""
	}
	var labels []string
	for index := 0; index+1 < len(values); index += 2 {
		labels = append(labels, values[index]+`="`+values[index+1]+`"`)
	}
	sort.Strings(labels)
	return "{" + strings.Join(labels, ",") + "}"
}

func normalizeComponent(value Component) Component {
	for _, allowed := range allComponents {
		if value == allowed {
			return value
		}
	}
	return Component("unknown")
}

func normalizeResult(value string) string {
	return normalize(value, []string{"success", "error", "cancelled"}, "error")
}

func normalizeClaimResult(value string) string {
	return normalize(value, []string{"acquired", "empty", "contended", "error"}, "error")
}

func normalizeLeaseResult(value string) string {
	return normalize(value, []string{"heartbeat", "completed", "reclaimed", "lost", "error"}, "error")
}

func normalizeRetryReason(value string) string {
	return normalize(value, []string{
		"stale_lease", "version_conflict", "step_failure", "contention",
		"delivery_failure", "operator_retry", "worker_lost",
	}, "contention")
}

func normalizeWorkState(value string) string {
	return normalize(value, []string{
		"completed", "partial", "failed", "cancelled", "blocked", "dead_letter",
	}, "failed")
}

func normalizeComponentState(value string) string {
	return normalize(value, []string{"disabled", "starting", "idle", "active", "busy", "degraded", "stopped"}, "degraded")
}

func normalizeStuckKind(value string) string {
	return normalize(value, []string{"expired_lease", "lease_lost"}, "lease_lost")
}

func normalizeQueueState(value string) string {
	return normalize(value, []string{
		"pending", "delivering", "retry", "delivered", "dead_letter",
	}, "pending")
}

func normalize(value string, allowed []string, fallback string) string {
	for _, item := range allowed {
		if value == item {
			return value
		}
	}
	return fallback
}
