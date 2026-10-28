// Package control is the overlay-safe facade over Community data.
// Overlay modules must not import thewolf/internal.
package control

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Role  string `json:"role"`
	Name  string `json:"name,omitempty"`
}

type Record struct {
	Kind      string    `json:"kind"`
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Users interface {
	List(ctx context.Context) ([]User, error)
	Get(ctx context.Context, id string) (*User, error)
	GetByEmail(ctx context.Context, email string) (*User, error)
	Create(ctx context.Context, email, name, role string) (*User, error)
	Update(ctx context.Context, id, email, name, role string) (*User, error)
	Delete(ctx context.Context, id string) error
}

type Records interface {
	List(ctx context.Context, kind string) ([]Record, error)
	Get(ctx context.Context, kind, id string) (*Record, error)
	Put(ctx context.Context, rec Record) error
	Delete(ctx context.Context, kind, id string) error
}

type Settings interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string) error
	List(ctx context.Context) (map[string]string, error)
}

// Fleet is read-only fleet evidence for overlay reports. Report covers every
// repository (admin scope), so overlays serve it only when IsAdmin holds.
type Fleet interface {
	// Report is the GET /fleet/report JSON for all repositories, or one collection.
	Report(ctx context.Context, collectionID string) (json.RawMessage, error)
	// IsAdmin reports whether the request's caller holds the administrator role.
	IsAdmin(ctx context.Context) bool
}

// SealedPrefix marks a value produced by Sealer.Seal. Values without it are
// legacy plaintext.
const SealedPrefix = "wolf-sealed:v1:"

// Sealer encrypts overlay secrets at rest with the Community master key.
type Sealer interface {
	Seal(plaintext string) (string, error)
	Open(sealed string) (string, error)
}

var (
	mu       sync.RWMutex
	sealer   Sealer
	fleet    Fleet
	users    Users
	records  Records
	settings Settings
)

func Bind(u Users, r Records, s Settings) {
	mu.Lock()
	users, records, settings = u, r, s
	mu.Unlock()
}

func UsersAPI() Users {
	mu.RLock()
	defer mu.RUnlock()
	return users
}

func RecordsAPI() Records {
	mu.RLock()
	defer mu.RUnlock()
	return records
}

func SettingsAPI() Settings {
	mu.RLock()
	defer mu.RUnlock()
	return settings
}

func BindFleet(f Fleet) {
	mu.Lock()
	fleet = f
	mu.Unlock()
}

func FleetAPI() Fleet {
	mu.RLock()
	defer mu.RUnlock()
	return fleet
}

func BindSealer(s Sealer) {
	mu.Lock()
	sealer = s
	mu.Unlock()
}

func SealerAPI() Sealer {
	mu.RLock()
	defer mu.RUnlock()
	return sealer
}
