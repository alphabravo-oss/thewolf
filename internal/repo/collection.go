package repo

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
)

// CollectionManager handles CRUD for collections.
type CollectionManager struct {
	store db.Store
}

// NewCollectionManager creates a new CollectionManager.
func NewCollectionManager(store db.Store) *CollectionManager {
	return &CollectionManager{store: store}
}

// Create creates a new collection.
func (m *CollectionManager) Create(ctx context.Context, col *models.Collection) error {
	return m.store.CreateCollection(ctx, col)
}

// Get retrieves a collection by ID.
func (m *CollectionManager) Get(ctx context.Context, id string) (*models.Collection, error) {
	return m.store.GetCollectionByID(ctx, id)
}

// List returns all collections for a user.
func (m *CollectionManager) List(ctx context.Context, userID string) ([]models.Collection, error) {
	return m.store.ListCollectionsByUser(ctx, userID)
}

// Update updates a collection.
func (m *CollectionManager) Update(ctx context.Context, col *models.Collection) error {
	return m.store.UpdateCollection(ctx, col)
}

// Delete deletes a collection.
func (m *CollectionManager) Delete(ctx context.Context, id string) error {
	return m.store.DeleteCollection(ctx, id)
}

// AddRepo adds a repo to a collection.
func (m *CollectionManager) AddRepo(ctx context.Context, collectionID, repoID string) error {
	return m.store.AddRepoToCollection(ctx, collectionID, repoID)
}

// RemoveRepo removes a repo from a collection.
func (m *CollectionManager) RemoveRepo(ctx context.Context, collectionID, repoID string) error {
	return m.store.RemoveRepoFromCollection(ctx, collectionID, repoID)
}

// ListRepos lists all repos in a collection.
func (m *CollectionManager) ListRepos(ctx context.Context, collectionID string) ([]models.Repo, error) {
	return m.store.ListReposInCollection(ctx, collectionID)
}

// ResolveRepoPath returns the actual filesystem path for a repo.
// For local repos, it returns SourcePath directly.
// For remote repos (github, gitlab, git), it returns the cached clone path
// under ~/.wolf/cache/repos/<source>/<path>.
func ResolveRepoPath(r models.Repo) (string, error) {
	switch r.SourceType {
	case models.SourceTypeLocal:
		return r.SourcePath, nil
	case models.SourceTypeGitHub, models.SourceTypeGitLab, models.SourceTypeGit:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("get home dir: %w", err)
		}
		cachePath := filepath.Join(home, ".wolf", "cache", "repos", string(r.SourceType), r.Name)
		return cachePath, nil
	default:
		return "", fmt.Errorf("unknown source type %q for repo %s", r.SourceType, r.Name)
	}
}
