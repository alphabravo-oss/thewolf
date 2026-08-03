package db

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jmoiron/sqlx"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func enforceDurableScanEventPolicy(ctx context.Context, tx *sqlx.Tx, event *models.ScanEvent) error {
	if event.EventType != models.ScanEventTypeToolOutput &&
		event.EventType != models.ScanEventTypeToolOutputTruncated &&
		event.EventType != models.ScanEventTypeToolOutputDropped {
		return nil
	}

	if len(event.DataJSON) > models.MaxDurableScanEventDataBytes {
		var original struct {
			ToolName string `json:"tool_name"`
		}
		_ = json.Unmarshal([]byte(event.DataJSON), &original)
		payload, _ := json.Marshal(map[string]interface{}{
			"type":           models.ScanEventTypeToolOutput,
			"scan_id":        event.ScanID,
			"tool_name":      original.ToolName,
			"line":           fmt.Sprintf("[durable output line omitted: %d bytes exceeds the %d-byte event limit; complete output remains in scan artifacts]", len(event.DataJSON), models.MaxDurableScanEventDataBytes),
			"truncated":      true,
			"reason":         "event_too_large",
			"original_bytes": len(event.DataJSON),
			"max_bytes":      models.MaxDurableScanEventDataBytes,
		})
		event.EventType = models.ScanEventTypeToolOutputTruncated
		event.DataJSON = string(payload)
	}

	var count int
	if err := tx.GetContext(ctx, &count, tx.Rebind(
		`SELECT COUNT(*) FROM scan_events
		 WHERE scan_id = ? AND event_type IN (?, ?, ?)`),
		event.ScanID,
		models.ScanEventTypeToolOutput,
		models.ScanEventTypeToolOutputTruncated,
		models.ScanEventTypeToolOutputDropped,
	); err != nil {
		return err
	}
	if count < models.MaxDurableToolOutputEventsPerScan {
		return nil
	}
	if count > models.MaxDurableToolOutputEventsPerScan ||
		event.EventType == models.ScanEventTypeToolOutputDropped {
		return ErrScanEventDropped
	}

	var original struct {
		ToolName string `json:"tool_name"`
	}
	_ = json.Unmarshal([]byte(event.DataJSON), &original)
	payload, _ := json.Marshal(map[string]interface{}{
		"type":       models.ScanEventTypeToolOutput,
		"scan_id":    event.ScanID,
		"tool_name":  original.ToolName,
		"line":       fmt.Sprintf("[durable output stopped after %d events; complete output remains in scan artifacts]", models.MaxDurableToolOutputEventsPerScan),
		"truncated":  true,
		"reason":     "event_limit",
		"max_events": models.MaxDurableToolOutputEventsPerScan,
	})
	event.EventType = models.ScanEventTypeToolOutputDropped
	event.DataJSON = string(payload)
	return nil
}
