package adapters_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

type ownedFunctionRepository struct {
	contractRepository

	invocation adapters.Invocation
}

func (repo *ownedFunctionRepository) Admit(
	ctx context.Context,
	prepared adapters.PreparedInvocation,
) (adapters.Invocation, error) {
	if err := ctx.Err(); err != nil {
		return adapters.Invocation{}, err
	}
	function := prepared.Function()
	repo.invocation = adapters.Invocation{ID: prepared.ID(), Function: function.Descriptor,
		CatalogRevision: function.CatalogRevision, Route: adapters.RouteSnapshot{AdapterID: function.AdapterID},
		Arguments: prepared.Arguments(), StartedAt: prepared.StartedAt(), Deadline: prepared.Deadline()}
	return repo.invocation, nil
}

func (repo *ownedFunctionRepository) Complete(
	ctx context.Context,
	id adapters.InvocationID,
	evidence adapters.Evidence,
	at time.Time,
) (adapters.Invocation, error) {
	if err := ctx.Err(); err != nil {
		return adapters.Invocation{}, err
	}
	if id != repo.invocation.ID {
		return adapters.Invocation{}, adapters.ErrInvocationNotFound
	}
	repo.invocation.Completion = &adapters.Completion{At: at, Evidence: evidence}
	return repo.invocation, nil
}

type functionInvokerFunc func(context.Context, adapters.Invocation) adapters.Evidence

func (invoke functionInvokerFunc) Invoke(ctx context.Context, invocation adapters.Invocation) adapters.Evidence {
	return invoke(ctx, invocation)
}

//nolint:gocognit // One barrier-controlled lifecycle scenario exercises both entry points through terminal evidence.
func TestCommittedFunctionOutlivesCallerAndRemainsDrainTracked(t *testing.T) {
	t.Parallel()
	for _, public := range []bool{true, false} {
		t.Run(map[bool]string{true: "public", false: "reserved"}[public], func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				repo := &ownedFunctionRepository{}
				repo.function = adapters.Function{
					AdapterID:       "bridge",
					CatalogRevision: "afc_01890f47-7a6b-7c4d-8e9f-0123456789ab",
					Descriptor:      contractDescriptor(),
				}
				entered := make(chan adapters.Invocation, 1)
				release := make(chan struct{})
				invoker := functionInvokerFunc(
					func(ctx context.Context, invocation adapters.Invocation) adapters.Evidence {
						entered <- invocation
						<-release
						if err := ctx.Err(); err != nil {
							t.Errorf("owned dispatch canceled: %v", err)
						}
						return adapters.Succeeded{Result: json.RawMessage(`{"n":9007199254740993}`)}
					},
				)
				service, err := adapters.NewFunctionService(t.Context(), repo, invoker)
				if err != nil {
					t.Fatal(err)
				}
				caller, cancel := context.WithCancel(t.Context())
				defer cancel()
				input := adapters.InvokeInput{
					AdapterID:       "bridge",
					FunctionKey:     "inspect",
					CatalogRevision: repo.function.CatalogRevision,
					Arguments:       json.RawMessage(` {"n":9007199254740993} `),
				}
				const reservedID = adapters.InvocationID("afi_01890f47-7a6b-7c4d-8e9f-0123456789ab")
				contract := adapters.ContractInput{
					AdapterID:           "bridge",
					FunctionKey:         "inspect",
					ContractFingerprint: knownFingerprint,
					Arguments:           input.Arguments,
				}
				done := make(chan struct{})
				var terminal adapters.Invocation
				var invokeErr error
				go func() {
					defer close(done)
					if public {
						terminal, invokeErr = service.Invoke(caller, input)
					} else {
						terminal, invokeErr = service.InvokeReserved(
							caller,
							adapters.ReservedInvokeInput{
								InvocationID:  reservedID,
								ContractInput: contract,
							},
						)
					}
				}()
				dispatched := <-entered
				if string(dispatched.Arguments) != `{"n":9007199254740993}` || dispatched.Route.AdapterID != "bridge" ||
					dispatched.Function.Key != "inspect" || !dispatched.Deadline.Equal(dispatched.StartedAt.Add(time.Second)) {
					t.Fatalf("dispatch=%+v", dispatched)
				}
				if public {
					if _, err = adapters.ParseInvocationID(string(dispatched.ID)); err != nil {
						t.Fatal(err)
					}
				} else if dispatched.ID != reservedID {
					t.Fatalf("reserved identity changed: %s", dispatched.ID)
				}
				cancel()
				service.ClosePublicAdmission()
				if _, err = service.Invoke(t.Context(), input); !errors.Is(err, adapters.ErrFunctionAdmissionClosed) {
					t.Fatalf("public admission=%v", err)
				}
				if err = service.Drain(caller); !errors.Is(err, context.Canceled) {
					t.Fatalf("drain lost in-flight ownership: %v", err)
				}
				select {
				case <-done:
					t.Fatal("work completed before dispatch settled")
				default:
				}
				close(release)
				<-done
				if invokeErr != nil || terminal.ID != dispatched.ID || terminal.Completion == nil {
					t.Fatalf("terminal=%+v err=%v", terminal, invokeErr)
				}
				if outcome, ok := terminal.Completion.Evidence.(adapters.Succeeded); !ok ||
					string(outcome.Result) != `{"n":9007199254740993}` {
					t.Fatalf("terminal evidence=%+v", terminal.Completion.Evidence)
				}
				if err = service.Drain(t.Context()); err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

type blockedFunctionLookupRepository struct{ contractRepository }

func (*blockedFunctionLookupRepository) GetFunction(
	ctx context.Context, _ string, _ adapters.FunctionKey,
) (adapters.Function, error) {
	timer := time.NewTimer(adapters.FunctionPersistenceTimeout + time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return adapters.Function{}, ctx.Err()
	case <-timer.C:
		return adapters.Function{}, errors.New("lookup exceeded persistence budget")
	}
}

func TestReservedFunctionLookupIsBoundedAndReleasesAdmission(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		service, err := adapters.NewFunctionService(t.Context(), &blockedFunctionLookupRepository{}, contractInvoker{})
		if err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		contract := adapters.ContractInput{AdapterID: "bridge", FunctionKey: "inspect",
			ContractFingerprint: knownFingerprint, Arguments: json.RawMessage(`{}`)}
		_, err = service.InvokeReserved(context.WithoutCancel(t.Context()), adapters.ReservedInvokeInput{
			InvocationID:  "afi_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			ContractInput: contract,
		})
		if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, adapters.ErrInvalidFunctionInput) ||
			time.Since(started) != adapters.FunctionPersistenceTimeout {
			t.Errorf("unbounded or misclassified lookup: elapsed=%s err=%v", time.Since(started), err)
		}
		drainContext, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if err = service.Drain(drainContext); err != nil {
			t.Fatalf("timed-out lookup retained admission: %v", err)
		}
	})
}
