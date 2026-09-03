// Package control is the overlay-safe facade over Community data.
// Overlay modules must not import thewolf/internal.
package control

import (
	"context"
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

var (
	mu       sync.RWMutex
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
