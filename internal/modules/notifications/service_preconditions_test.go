package notifications_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

func TestConstructionPreconditionsAreNotInvalidInput(t *testing.T) {
	t.Parallel()
	for _, missing := range []string{"repository", "sender"} {
		t.Run(missing, func(t *testing.T) {
			t.Parallel()
			repository := &constructionRepository{}
			var repo notifications.Repository = repository
			var sender notifications.Sender = policySender(func(context.Context, notifications.Attempt) notifications.Evidence {
				t.Error("unexpected dispatch")
				return notifications.Evidence{}
			})
			if missing == "repository" {
				repo = nil
			} else {
				sender = nil
			}
			service, err := notifications.NewService(t.Context(), repo, sender)
			if service != nil || err == nil || errors.Is(err, notifications.ErrInvalidInput) ||
				errors.Is(err, notifications.ErrPersistence) {
				t.Errorf("construction error=%v service=%v, want internal configuration failure", err, service)
			}
			if repository.recoveries != 0 {
				t.Fatalf("invalid construction attempted %d recoveries", repository.recoveries)
			}
		})
	}
}

func TestConstructionPreservesRecoveryFailureIdentity(t *testing.T) {
	t.Parallel()
	for _, cause := range []error{errors.New("private recovery failure"), context.Canceled} {
		t.Run(cause.Error(), func(t *testing.T) {
			t.Parallel()
			repository := &constructionRepository{failure: cause}
			service, err := notifications.NewService(
				t.Context(),
				repository,
				policySender(
					func(context.Context, notifications.Attempt) notifications.Evidence { return notifications.Evidence{} },
				),
			)
			if service != nil || !errors.Is(err, cause) || !errors.Is(err, notifications.ErrPersistence) ||
				repository.recoveries != 1 {
				t.Fatalf("recovery result service=%v error=%v calls=%d", service, err, repository.recoveries)
			}
		})
	}
}

type constructionRepository struct {
	notifications.Repository

	failure    error
	recoveries int
}

func (repo *constructionRepository) Recover(context.Context, time.Time) error {
	repo.recoveries++
	return repo.failure
}

func TestRetentionClockFailureIsNotInvalidInput(t *testing.T) {
	t.Parallel()
	service := newPolicyService(
		t,
		&policyRepository{},
		func(context.Context, notifications.Attempt) notifications.Evidence { return notifications.Evidence{} },
	)
	if err := service.PruneHistory(
		t.Context(),
		time.Time{},
	); err == nil ||
		errors.Is(err, notifications.ErrInvalidInput) {
		t.Fatalf("retention clock error=%v, want internal precondition failure", err)
	}
}
