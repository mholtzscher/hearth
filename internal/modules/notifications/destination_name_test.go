package notifications_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

func TestDestinationNamePreparationPreservesRegistrationContract(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ name, input, want string }{
		{"trim whitespace", " Home ", "Home"},
		{"Unicode bound", strings.Repeat("暖", 200), strings.Repeat("暖", 200)},
		{"blank", " \t", ""},
		{"over bound", strings.Repeat("暖", 201), ""},
		{"invalid UTF-8", string([]byte{0xff}), ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			name, nameErr := notifications.PrepareDestinationName(test.input)
			registration, registrationErr := notifications.PrepareRegistration(notifications.Registration{
				AdapterID: "alerts",
				RuntimeID: "run_01890f47-7a6b-7c4d-8e9f-0123456789ab",
				Key:       "home",
				Name:      test.input,
			})
			if test.want == "" {
				if !errors.Is(nameErr, notifications.ErrInvalidInput) ||
					!errors.Is(registrationErr, notifications.ErrInvalidInput) {
					t.Fatalf("invalid name errors: preparation=%v registration=%v", nameErr, registrationErr)
				}
				return
			}
			if nameErr != nil || registrationErr != nil || name != test.want || registration.Name != test.want {
				t.Fatalf("name=%q registration=%+v errors=%v/%v", name, registration, nameErr, registrationErr)
			}
		})
	}
}
