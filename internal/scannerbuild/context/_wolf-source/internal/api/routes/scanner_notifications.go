package routes

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

const maxNotificationRetryReasonBytes = 1000

// ScannerSupplyChainListNotifications lists both the administrator UI records
// and external delivery attempts. Destination references are opaque aliases;
// endpoint addresses and credentials are never persisted or returned.
func ScannerSupplyChainListNotifications(w http.ResponseWriter, r *http.Request) {
	filter, ok := scannerNotificationFilter(w, r)
	if !ok {
		return
	}
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	page, err := store.ListNotifications(r.Context(), filter, scannerPage(r))
	if err != nil {
		response.WriteError(w, http.StatusBadRequest, "invalid_cursor", err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	response.WriteJSON(w, http.StatusOK, scannerCursorResponse{
		Data: page.Items, Meta: scannerCursorMeta{NextCursor: page.NextCursor},
	})
}

func ScannerSupplyChainGetNotification(w http.ResponseWriter, r *http.Request) {
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	notification, err := store.GetNotification(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, notification.Version))
	w.Header().Set("Cache-Control", "no-store")
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: notification})
}

func ScannerSupplyChainRetryNotification(w http.ResponseWriter, r *http.Request) {
	idempotencyKey, ok := scannerIdempotencyKey(w, r)
	if !ok {
		return
	}
	version, ok := scannerExpectedVersion(w, r)
	if !ok {
		return
	}
	var request struct {
		Reason string `json:"reason"`
	}
	if !scannerDecode(w, r, &request) {
		return
	}
	request.Reason = strings.TrimSpace(request.Reason)
	if request.Reason == "" || len(request.Reason) > maxNotificationRetryReasonBytes {
		response.WriteError(
			w, http.StatusBadRequest, "invalid_reason",
			"reason is required and must be at most 1000 bytes",
		)
		return
	}
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	notification, err := store.RetryDeadLetterNotification(
		r.Context(), chi.URLParam(r, "id"), version,
		scannerrelease.TransitionCommand{
			Actor:          scannerActor(r),
			Reason:         request.Reason,
			IdempotencyKey: idempotencyKey,
		},
		time.Now().UTC(),
	)
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, notification.Version))
	w.Header().Set("Cache-Control", "no-store")
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: notification})
}

func scannerNotificationFilter(
	w http.ResponseWriter,
	r *http.Request,
) (scannerrelease.NotificationFilter, bool) {
	filter := scannerrelease.NotificationFilter{
		State: scannerrelease.NotificationState(
			strings.TrimSpace(r.URL.Query().Get("state")),
		),
		DestinationType: scannerrelease.NotificationDestinationType(
			strings.TrimSpace(r.URL.Query().Get("destination_type")),
		),
		NotificationType: strings.TrimSpace(r.URL.Query().Get("notification_type")),
	}
	switch filter.State {
	case "", scannerrelease.NotificationPending,
		scannerrelease.NotificationDelivering, scannerrelease.NotificationRetry,
		scannerrelease.NotificationDelivered, scannerrelease.NotificationDeadLetter:
	default:
		response.WriteError(w, http.StatusBadRequest, "invalid_filter", "unsupported notification state")
		return scannerrelease.NotificationFilter{}, false
	}
	switch filter.DestinationType {
	case "", scannerrelease.NotificationDestinationUI,
		scannerrelease.NotificationDestinationWebhook,
		scannerrelease.NotificationDestinationEmail,
		scannerrelease.NotificationDestinationSIEM:
	default:
		response.WriteError(w, http.StatusBadRequest, "invalid_filter", "unsupported notification destination_type")
		return scannerrelease.NotificationFilter{}, false
	}
	if len(filter.NotificationType) > 100 ||
		strings.ContainsAny(filter.NotificationType, "\x00\r\n") {
		response.WriteError(w, http.StatusBadRequest, "invalid_filter", "notification_type is invalid")
		return scannerrelease.NotificationFilter{}, false
	}
	return filter, true
}
