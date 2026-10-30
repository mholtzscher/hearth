package notifications_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

func TestNotificationPersistenceClassesAreStageScoped(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"read", "registration", "initial", "terminal"} {
		for _, cause := range []error{errors.New("private storage"), context.Canceled, context.DeadlineExceeded} {
			t.Run(stage+"/"+cause.Error(), func(t *testing.T) {
				t.Parallel()
				exerciseNotificationPersistenceContract(t, stage, cause)
			})
		}
	}
}

func exerciseNotificationPersistenceContract(t *testing.T, stage string, cause error) {
	t.Helper()
	var sends int
	repo := &persistenceContractRepository{cause: cause}
	repo.admit = func(_ context.Context, in notifications.SendInput, start, deadline time.Time) (notifications.Attempt, bool, error) {
		if stage == "initial" {
			return notifications.Attempt{}, false, cause
		}
		return notifications.Attempt{
			ID:            in.AttemptID,
			DestinationID: in.DestinationID,
			Message:       in.Message,
			StartedAt:     start,
			Deadline:      deadline,
		}, true, nil
	}
	repo.complete = func(context.Context, notifications.AttemptID, notifications.Evidence, time.Time) (notifications.Attempt, error) {
		return notifications.Attempt{}, cause
	}
	service, err := notifications.NewService(
		t.Context(),
		repo,
		policySender(func(context.Context, notifications.Attempt) notifications.Evidence {
			sends++
			return notifications.Evidence{Outcome: notifications.Accepted}
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	in := policyInput(t)
	switch stage {
	case "read":
		_, err = service.GetDestination(t.Context(), in.DestinationID)
	case "registration":
		_, err = service.Register(
			t.Context(),
			notifications.Registration{
				AdapterID: "alerts",
				RuntimeID: "run_01890f47-7a6b-7c4d-8e9f-0123456789ab",
				Key:       "home",
				Name:      "Home",
			},
		)
	case "initial", "terminal":
		_, err = service.Send(t.Context(), in)
	}
	wantPersistence := stage == "initial" || stage == "terminal"
	if !errors.Is(err, cause) || errors.Is(err, notifications.ErrPersistence) != wantPersistence {
		t.Fatalf("stage=%s identity/class changed: %v", stage, err)
	}
	wantSends := 0
	if stage == "terminal" {
		wantSends = 1
	}
	if sends != wantSends {
		t.Fatalf("dispatch=%d want=%d", sends, wantSends)
	}
}

type persistenceContractRepository struct {
	policyRepository

	cause error
}

func (repo *persistenceContractRepository) GetDestination(
	context.Context,
	notifications.DestinationID,
) (notifications.Destination, error) {
	return notifications.Destination{}, repo.cause
}

func (repo *persistenceContractRepository) Register(
	context.Context,
	notifications.Registration,
) (notifications.Destination, error) {
	return notifications.Destination{}, repo.cause
}
