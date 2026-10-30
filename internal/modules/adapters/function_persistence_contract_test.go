package adapters_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

func TestFunctionPersistenceClassesAreStageScoped(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"read", "initial", "terminal"} {
		for _, cause := range []error{errors.New("private storage"), context.Canceled, context.DeadlineExceeded} {
			t.Run(stage+"/"+cause.Error(), func(t *testing.T) {
				t.Parallel()
				exerciseFunctionPersistenceContract(t, stage, cause)
			})
		}
	}
}

func exerciseFunctionPersistenceContract(t *testing.T, stage string, cause error) {
	t.Helper()
	repo := &persistenceFunctionRepository{stage: stage, cause: cause}
	repo.function = adapters.Function{
		AdapterID:       "bridge",
		CatalogRevision: "afc_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		Descriptor:      contractDescriptor(),
	}
	sends := 0
	service, err := adapters.NewFunctionService(
		t.Context(),
		repo,
		functionInvokerFunc(func(context.Context, adapters.Invocation) adapters.Evidence {
			sends++
			return adapters.Succeeded{Result: json.RawMessage(`{}`)}
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.Invoke(
		t.Context(),
		adapters.InvokeInput{
			AdapterID:       "bridge",
			FunctionKey:     "inspect",
			CatalogRevision: repo.function.CatalogRevision,
			Arguments:       json.RawMessage(`{}`),
		},
	)
	if !errors.Is(err, cause) || errors.Is(err, adapters.ErrFunctionPersistence) != (stage == "terminal") ||
		got.ID != "" {
		t.Fatalf("stage=%s invented evidence/class: %+v %v", stage, got, err)
	}
	wantSends := 0
	if stage == "terminal" {
		wantSends = 1
		failure, ok := errors.AsType[*adapters.InvocationError](err)
		if !ok || failure.ID != repo.invocation.ID {
			t.Fatalf("terminal failure lost durable identity: %v", err)
		}
	}
	if sends != wantSends {
		t.Fatalf("dispatch=%d want=%d", sends, wantSends)
	}
}

type persistenceFunctionRepository struct {
	ownedFunctionRepository

	stage string
	cause error
}

func (repo *persistenceFunctionRepository) GetFunction(
	ctx context.Context,
	id string,
	key adapters.FunctionKey,
) (adapters.Function, error) {
	if repo.stage == "read" {
		return adapters.Function{}, repo.cause
	}
	return repo.contractRepository.GetFunction(ctx, id, key)
}

func (repo *persistenceFunctionRepository) Admit(
	ctx context.Context,
	in adapters.PreparedInvocation,
) (adapters.Invocation, error) {
	if repo.stage == "initial" {
		return adapters.Invocation{}, repo.cause
	}
	return repo.ownedFunctionRepository.Admit(ctx, in)
}

func (repo *persistenceFunctionRepository) Complete(
	context.Context,
	adapters.InvocationID,
	adapters.Evidence,
	time.Time,
) (adapters.Invocation, error) {
	return adapters.Invocation{}, repo.cause
}
