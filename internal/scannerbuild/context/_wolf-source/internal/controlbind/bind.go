package controlbind

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/pkg/control"
	"golang.org/x/crypto/bcrypt"
)

type users struct{ store db.Store }
type records struct{ store db.Store }
type settings struct{ store db.Store }

func Bind(store db.Store) {
	control.Bind(users{store}, records{store}, settings{store})
}

func (u users) List(ctx context.Context) ([]control.User, error) {
	all, err := u.store.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]control.User, 0, len(all))
	for i := range all {
		out = append(out, toUser(&all[i]))
	}
	return out, nil
}

func (u users) Get(ctx context.Context, id string) (*control.User, error) {
	row, err := u.store.GetUserByID(ctx, id)
	if err != nil || row == nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	cu := toUser(row)
	return &cu, nil
}

func (u users) GetByEmail(ctx context.Context, email string) (*control.User, error) {
	row, err := u.store.GetUserByEmail(ctx, strings.ToLower(strings.TrimSpace(email)))
	if err != nil || row == nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	cu := toUser(row)
	return &cu, nil
}

func (u users) Create(ctx context.Context, email, name, role string) (*control.User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || !strings.Contains(email, "@") {
		return nil, errors.New("valid email is required")
	}
	if role != models.RoleAdmin {
		role = models.RoleUser
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	hash, err := bcrypt.GenerateFromPassword(buf, bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	row := &models.User{
		ID:           hex.EncodeToString(buf),
		Email:        email,
		DisplayName:  strings.TrimSpace(name),
		Role:         role,
		PasswordHash: string(hash),
	}
	if err := u.store.CreateUser(ctx, row); err != nil {
		return nil, err
	}
	if row.DisplayName != "" {
		_ = u.store.UpdateUserProfile(ctx, row)
	}
	cu := toUser(row)
	return &cu, nil
}

func (u users) Update(ctx context.Context, id, email, name, role string) (*control.User, error) {
	row, err := u.store.GetUserByID(ctx, id)
	if err != nil || row == nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if e := strings.ToLower(strings.TrimSpace(email)); e != "" {
		row.Email = e
	}
	if name != "" {
		row.DisplayName = strings.TrimSpace(name)
	}
	if role == models.RoleAdmin || role == models.RoleUser {
		row.Role = role
	}
	if err := u.store.UpdateUser(ctx, row); err != nil {
		return nil, err
	}
	if err := u.store.UpdateUserProfile(ctx, row); err != nil {
		return nil, err
	}
	cu := toUser(row)
	return &cu, nil
}

func (u users) Delete(ctx context.Context, id string) error {
	return u.store.DeleteUser(ctx, id)
}

func (r records) List(ctx context.Context, kind string) ([]control.Record, error) {
	rows, err := r.store.ListEnterpriseRecords(ctx, kind)
	if err != nil {
		return nil, err
	}
	out := make([]control.Record, 0, len(rows))
	for i := range rows {
		out = append(out, toRec(&rows[i]))
	}
	return out, nil
}

func (r records) Get(ctx context.Context, kind, id string) (*control.Record, error) {
	row, err := r.store.GetEnterpriseRecord(ctx, kind, id)
	if err != nil || row == nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	rec := toRec(row)
	return &rec, nil
}

func (r records) Put(ctx context.Context, rec control.Record) error {
	return r.store.PutEnterpriseRecord(ctx, &models.EnterpriseRecord{
		Kind: rec.Kind, ID: rec.ID, Name: rec.Name, Body: rec.Body,
	})
}

func (r records) Delete(ctx context.Context, kind, id string) error {
	return r.store.DeleteEnterpriseRecord(ctx, kind, id)
}

func (s settings) Get(ctx context.Context, key string) (string, error) {
	return s.store.GetSetting(ctx, key)
}

func (s settings) Set(ctx context.Context, key, value string) error {
	return s.store.SetSetting(ctx, key, value)
}

func (s settings) List(ctx context.Context) (map[string]string, error) {
	return s.store.ListSettings(ctx)
}

func toUser(u *models.User) control.User {
	return control.User{ID: u.ID, Email: u.Email, Role: u.Role, Name: u.DisplayName}
}

func toRec(r *models.EnterpriseRecord) control.Record {
	return control.Record{Kind: r.Kind, ID: r.ID, Name: r.Name, Body: r.Body, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}
