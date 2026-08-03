package routes

import (
	"sync"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/plugin"
)

const (
	maxDurableScanStreamsPerUser = 8
	maxDurableScanStreamsPerScan = 2
)

// Handler holds shared dependencies for route handlers.
type Handler struct {
	Store         db.Store
	Registry      *plugin.Registry
	streamOnce    sync.Once
	streamLimiter *scanStreamLimiter
}

type scanStreamLimiter struct {
	mu     sync.Mutex
	byUser map[string]int
	byScan map[string]int
}

func newScanStreamLimiter() *scanStreamLimiter {
	return &scanStreamLimiter{
		byUser: make(map[string]int),
		byScan: make(map[string]int),
	}
}

func (h *Handler) durableScanStreamLimiter() *scanStreamLimiter {
	h.streamOnce.Do(func() {
		if h.streamLimiter == nil {
			h.streamLimiter = newScanStreamLimiter()
		}
	})
	return h.streamLimiter
}

func (l *scanStreamLimiter) acquire(userID, scanID string) (func(), bool) {
	if l == nil || userID == "" || scanID == "" {
		return func() {}, false
	}
	l.mu.Lock()
	if l.byUser[userID] >= maxDurableScanStreamsPerUser ||
		l.byScan[scanID] >= maxDurableScanStreamsPerScan {
		l.mu.Unlock()
		return func() {}, false
	}
	l.byUser[userID]++
	l.byScan[scanID]++
	l.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			l.byUser[userID]--
			l.byScan[scanID]--
			if l.byUser[userID] == 0 {
				delete(l.byUser, userID)
			}
			if l.byScan[scanID] == 0 {
				delete(l.byScan, scanID)
			}
		})
	}, true
}

// DefaultHandler is the global handler instance set by the server.
var DefaultHandler *Handler

// SetHandler sets the global handler used by route functions.
func SetHandler(store db.Store, registry *plugin.Registry) {
	DefaultHandler = &Handler{
		Store:         store,
		Registry:      registry,
		streamLimiter: newScanStreamLimiter(),
	}
}
