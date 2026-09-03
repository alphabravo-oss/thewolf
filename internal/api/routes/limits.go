package routes

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/pkg/entitlement"
)

const (
	limitRepos   = "repos"
	limitUsers   = "users"
	limitWorkers = "workers"
)

type communityLimitError struct {
	code, msg string
}

func (e *communityLimitError) Error() string { return e.msg }

// CheckCommunityLimit is the serve-bootstrap entry for user ceilings.
func CheckCommunityLimit(ctx context.Context, store db.Store, kind string) error {
	return checkCommunityLimit(ctx, store, kind)
}

func checkCommunityLimit(ctx context.Context, store db.Store, kind string) error {
	if store == nil || !entitlement.EnforceCommunityLimits() {
		return nil
	}
	lim := entitlement.CommunityLimits()
	switch kind {
	case limitRepos:
		all, err := store.ListAllRepos(ctx)
		if err != nil {
			return err
		}
		if len(all) >= lim.Repos {
			return &communityLimitError{
				code: "community_limit",
				msg:  fmt.Sprintf("Community evaluation limit: at most %d repositories", lim.Repos),
			}
		}
	case limitUsers:
		all, err := store.ListUsers(ctx)
		if err != nil {
			return err
		}
		if len(all) >= lim.Users {
			return &communityLimitError{
				code: "community_limit",
				msg:  fmt.Sprintf("Community evaluation limit: at most %d users", lim.Users),
			}
		}
	case limitWorkers:
		// ponytail: full scan list, add a count query if the table grows
		all, err := store.ListAllScans(ctx)
		if err != nil {
			return err
		}
		n := 0
		for i := range all {
			switch all[i].Status {
			case models.ScanStatusPending, models.ScanStatusRunning:
				n++
			}
		}
		if n >= lim.Workers {
			return &communityLimitError{
				code: "community_limit",
				msg:  fmt.Sprintf("Community evaluation limit: at most %d concurrent scan worker", lim.Workers),
			}
		}
	}
	return nil
}

func rejectCommunityLimit(w http.ResponseWriter, ctx context.Context, store db.Store, kind string) bool {
	err := checkCommunityLimit(ctx, store, kind)
	if err == nil {
		return false
	}
	var le *communityLimitError
	if errors.As(err, &le) {
		response.WriteError(w, http.StatusConflict, le.code, le.msg)
		return true
	}
	response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to evaluate community limits")
	return true
}

func persistCommunityLimit(ctx context.Context, store db.Store, kind string) error {
	err := checkCommunityLimit(ctx, store, kind)
	if err == nil {
		return nil
	}
	var le *communityLimitError
	if errors.As(err, &le) {
		return persistErr(http.StatusConflict, le.code, le.msg)
	}
	return persistErr(http.StatusInternalServerError, "server_error", "failed to evaluate community limits")
}
