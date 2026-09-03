package routes

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/wolflog"
)

type notificationItem struct {
	Type  string    `json:"type"`
	Title string    `json:"title"`
	Href  string    `json:"href"`
	At    time.Time `json:"at"`
}

func ListNotifications(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	items := make([]notificationItem, 0, 20)
	if scans, err := h.Store.ListScansByUser(r.Context(), claims.UserID); err == nil {
		for i := range scans {
			s := scans[i]
			at := s.CreatedAt
			if s.UpdatedAt.After(at) {
				at = s.UpdatedAt
			}
			switch s.Status {
			case models.ScanStatusRunning:
				items = append(items, notificationItem{
					Type: "scan_running", Title: "Scan running", Href: "/scans/" + s.ID, At: at,
				})
			case models.ScanStatusFailed:
				items = append(items, notificationItem{
					Type: "scan_failed", Title: "Scan failed", Href: "/scans/" + s.ID, At: at,
				})
			case models.ScanStatusCompleted:
				if s.FailureCode != "" {
					items = append(items, notificationItem{
						Type: "scan_failed", Title: "Scan completed with failure", Href: "/scans/" + s.ID, At: at,
					})
				}
			}
		}
	}
	if jobs, err := h.Store.ListFixJobsByUser(r.Context(), claims.UserID, ""); err == nil {
		for i := range jobs {
			j := jobs[i]
			switch j.Status {
			case models.FixJobFailed:
				items = append(items, notificationItem{
					Type: "fix_failed", Title: "Fix failed", Href: "/fixes/" + j.ID, At: j.UpdatedAt,
				})
			case models.FixJobSucceeded:
				items = append(items, notificationItem{
					Type: "fix_completed", Title: "Fix completed", Href: "/fixes/" + j.ID, At: j.UpdatedAt,
				})
			}
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].At.After(items[j].At) })
	if len(items) > 20 {
		items = items[:20]
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]any{"items": items}})
}

var errOutboundDisabled = fmt.Errorf("outbound webhook not configured")

func TestOutboundWebhook(w http.ResponseWriter, r *http.Request) {
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	if err := deliverOutbound(h, "test", nil); err != nil {
		status := http.StatusBadGateway
		if err == errOutboundDisabled {
			status = http.StatusBadRequest
		}
		response.WriteError(w, status, "delivery_failed", err.Error())
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]any{"ok": true}})
}

func notifyOutbound(h *Handler, event string, payload any) {
	if err := deliverOutbound(h, event, payload); err != nil && err != errOutboundDisabled {
		wolflog.Warn().Err(err).Str("event", event).Msg("outbound webhook failed")
	}
}

func deliverOutbound(h *Handler, event string, payload any) error {
	if h == nil || h.Store == nil {
		return fmt.Errorf("handler not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	url, err := h.Store.GetSetting(ctx, "outbound_webhook_url")
	if err != nil || strings.TrimSpace(url) == "" {
		return errOutboundDisabled
	}
	body := map[string]any{"event": event}
	if payload != nil {
		body["data"] = payload
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSpace(url), bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if secret, serr := h.Store.GetSetting(ctx, "outbound_webhook_secret"); serr == nil && strings.TrimSpace(secret) != "" {
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write(raw)
		req.Header.Set("X-Wolf-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("outbound webhook status %d", resp.StatusCode)
	}
	return nil
}
