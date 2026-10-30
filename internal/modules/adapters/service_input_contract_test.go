package adapters //nolint:testpackage // Management reads share canonical predicates without inventing a universal input class.

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestAdapterManagementIdentityContract(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"", "BAD", "bad.adapter", strings.Repeat("a", 64), "a", strings.Repeat("a", 63)} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			cause := errors.New("storage fault")
			repo := &inputContractReads{cause: cause}
			service := NewService(Stores{Reads: repo}, Dependencies{})
			_, err := service.GetAdapter(t.Context(), id)
			valid := ValidateAdapterID(id) == nil
			if valid && (!errors.Is(err, cause) || repo.calls != 1) {
				t.Fatal("valid ID lost operational cause")
			}
			if !valid && (err == nil || repo.calls != 0) {
				t.Fatal("invalid ID reached storage")
			}
			_, pageErr := service.ListAdapters(t.Context(), ListAdaptersParams{Limit: 1, AfterID: &id})
			if !valid && !errors.Is(pageErr, ErrInvalidPage) {
				t.Fatal("cursor lost its scoped page class")
			}
		})
	}
	for _, cause := range []error{ErrAdapterNotFound, context.Canceled, context.DeadlineExceeded} {
		repo := &inputContractReads{cause: cause}
		service := NewService(Stores{Reads: repo}, Dependencies{})
		if _, err := service.GetAdapter(t.Context(), "bridge"); !errors.Is(err, cause) {
			t.Fatalf("read cause lost: %v", err)
		}
	}
}

type inputContractReads struct {
	ReadRepository

	cause error
	calls int
}

func (repo *inputContractReads) GetAdapter(context.Context, string) (AdapterInstance, error) {
	repo.calls++
	return AdapterInstance{}, repo.cause
}
func (repo *inputContractReads) ListAdapters(context.Context, ListAdaptersParams) (Page[AdapterInstance], error) {
	return Page[AdapterInstance]{}, repo.cause
}
