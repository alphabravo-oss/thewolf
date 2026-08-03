package routes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/api/sse"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/wolflog"
)

const (
	minDurableScanEventPollInterval = time.Second
	maxDurableScanEventPollInterval = 5 * time.Second
)

// publishScanEvent writes progress durably before using the in-process broker
// as a low-latency wake-up. Worker and API processes may be different, so the
// database is the source of truth and the broker is only an optimization.
func publishScanEvent(h *Handler, scanID, eventType, data string) {
	publishScanEventForLease(h, scanID, "", eventType, data)
}

func publishScanEventForLease(h *Handler, scanID, leaseToken, eventType, data string) {
	if h == nil || h.Store == nil || scanID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	event := &models.ScanEvent{
		ScanID:     scanID,
		EventType:  eventType,
		DataJSON:   data,
		LeaseToken: leaseToken,
	}
	if err := h.Store.AppendScanEvent(ctx, event); err != nil {
		if errors.Is(err, db.ErrScanEventDropped) {
			return
		}
		wolflog.Warn().Err(err).Str("scan_id", scanID).Str("event_type", eventType).
			Msg("failed to persist scan event")
		return
	}
	if SSEBroker != nil {
		SSEBroker.Publish("scan:"+scanID, sse.Event{
			Type: event.EventType,
			Data: event.DataJSON,
			ID:   strconv.FormatInt(event.Sequence, 10),
		})
	}
}

func streamDurableScanEvents(w http.ResponseWriter, r *http.Request, h *Handler, scanID, userID string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "streaming not supported")
		return
	}
	release, admitted := h.durableScanStreamLimiter().acquire(userID, scanID)
	if !admitted {
		w.Header().Set("Retry-After", "5")
		response.WriteError(w, http.StatusTooManyRequests, "stream_limit",
			"too many concurrent scan event streams")
		return
	}
	defer release()

	after := int64(0)
	if raw := r.Header.Get("Last-Event-ID"); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed >= 0 {
			after = parsed
		}
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	var notifications <-chan sse.Event
	broker := SSEBroker
	if broker != nil {
		topic := "scan:" + scanID
		clientID := uuid.NewString()
		client := broker.Subscribe(topic, clientID)
		defer broker.Unsubscribe(topic, clientID)
		notifications = client.Events
	}

	pollInterval := minDurableScanEventPollInterval
	for {
		events, err := h.Store.ListScanEvents(r.Context(), scanID, after, 200)
		if err != nil {
			return
		}
		for _, event := range events {
			fmt.Fprintf(w, "id: %d\n", event.Sequence)     // nosemgrep: go.lang.security.audit.xss.no-fprintf-to-responsewriter.no-fprintf-to-responsewriter
			fmt.Fprintf(w, "data: %s\n\n", event.DataJSON) // nosemgrep: go.lang.security.audit.xss.no-fprintf-to-responsewriter.no-fprintf-to-responsewriter
			flusher.Flush()
			after = event.Sequence
		}

		scan, err := h.Store.GetScanByID(r.Context(), scanID)
		if err != nil {
			return
		}
		if isTerminalScanStatus(scan.Status) && len(events) == 0 {
			return
		}
		if isTerminalScanStatus(scan.Status) || len(events) == 200 {
			pollInterval = minDurableScanEventPollInterval
			continue
		}
		if len(events) > 0 {
			pollInterval = minDurableScanEventPollInterval
		}

		timer := time.NewTimer(pollInterval)
		select {
		case <-r.Context().Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case _, open := <-notifications:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			if !open {
				notifications = nil
			}
		case <-timer.C:
		}
		if len(events) == 0 {
			pollInterval *= 2
			if pollInterval > maxDurableScanEventPollInterval {
				pollInterval = maxDurableScanEventPollInterval
			}
		}
	}
}

func isTerminalScanStatus(status models.ScanStatus) bool {
	return status == models.ScanStatusCompleted ||
		status == models.ScanStatusFailed ||
		status == models.ScanStatusCancelled
}

// watchDurableScanCancellation bridges cancellation stored by the API process
// to the contexts owned by a separate worker process.
func watchDurableScanCancellation(ctx context.Context, h *Handler, scanID string, cancelScan context.CancelFunc) func() {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
			}

			scan, err := h.Store.GetScanByID(ctx, scanID)
			if err == nil && (scan.CancelRequestedAt != nil || scan.Status == models.ScanStatusCancelled) {
				cancelScan()
				return
			}
			runs, err := h.Store.ListScannerRunRecords(ctx, scanID)
			if err != nil {
				continue
			}
			var cancelFuncs []context.CancelFunc
			activeScansMu.Lock()
			for _, run := range runs {
				if run.CancelRequestedAt == nil {
					continue
				}
				if cancelledTools[scanID] == nil {
					cancelledTools[scanID] = make(map[string]bool)
				}
				cancelledTools[scanID][run.ToolName] = true
				if cancelFn := activeToolCtxs[scanID][run.ToolName]; cancelFn != nil {
					cancelFuncs = append(cancelFuncs, cancelFn)
				}
			}
			activeScansMu.Unlock()
			for _, cancelFn := range cancelFuncs {
				cancelFn()
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() { close(done) })
	}
}

func queueToolCancellation(ctx context.Context, h *Handler, scanID, toolName string) bool {
	scan, err := h.Store.GetScanByID(ctx, scanID)
	if err != nil || isTerminalScanStatus(scan.Status) {
		return false
	}
	runs, _ := h.Store.ListScannerRunRecords(ctx, scanID)
	for _, run := range runs {
		if run.ToolName != toolName {
			continue
		}
		if run.Status != "queued" && run.Status != "pending" && run.Status != "running" {
			return false
		}
		return h.Store.RequestScannerRunCancellation(ctx, scanID, toolName, time.Now().UTC()) == nil
	}

	var selected []string
	_ = json.Unmarshal([]byte(scan.ToolsSelected), &selected)
	found := false
	for _, name := range selected {
		if name == toolName {
			found = true
			break
		}
	}
	if !found {
		return false
	}
	now := time.Now().UTC()
	record := &models.ScannerRunRecord{
		ID:                uuid.NewString(),
		ScanID:            scanID,
		ToolName:          toolName,
		Status:            "cancelled",
		CommandJSON:       "{}",
		ErrorMessage:      "cancelled by user",
		ParserStatus:      "not_run",
		ParserMessage:     "cancelled by user",
		CancelRequestedAt: &now,
		RequestedScope:    "{}",
		EffectiveScope:    "{}",
		FinishedAt:        &now,
	}
	return h.Store.UpsertScannerRunRecord(ctx, record) == nil
}
