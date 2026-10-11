package natswire_test

import (
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
)

func TestNotificationSubjectsUseExactRuntimeRoutes(t *testing.T) {
	t.Parallel()
	runtimeID := "run_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	registration := "hearth.v1.adapter.discord-main.runtime." + runtimeID + ".notification-destination.register"
	send := "hearth.v1.adapter.discord-main.runtime." + runtimeID + ".notification.send"
	got, err := natswire.NotificationRegistrationSubject("discord-main", runtimeID)
	if err != nil || got != registration {
		t.Fatalf("registration subject = %q, %v", got, err)
	}
	got, err = natswire.NotificationSendSubject("discord-main", runtimeID)
	if err != nil || got != send {
		t.Fatalf("send subject = %q, %v", got, err)
	}
	registrationRoute, err := natswire.ParseNotificationRegistrationSubject(registration)
	if err != nil || registrationRoute.AdapterID != "discord-main" || registrationRoute.RuntimeID != runtimeID {
		t.Fatalf("registration route = %#v, %v", registrationRoute, err)
	}
	sendRoute, err := natswire.ParseNotificationSendSubject(send)
	if err != nil || sendRoute.AdapterID != "discord-main" || sendRoute.RuntimeID != runtimeID {
		t.Fatalf("send route = %#v, %v", sendRoute, err)
	}
	for _, invalid := range []string{send + ".extra", strings.Replace(send, "discord-main", "*", 1), strings.Replace(send, runtimeID, "*", 1), strings.Replace(send, ".runtime.", ".wrong.", 1), strings.Replace(send, ".send", ".register", 1), "hearth.v2.adapter.discord-main.runtime." + runtimeID + ".notification.send"} {
		if _, parseErr := natswire.ParseNotificationSendSubject(invalid); parseErr == nil {
			t.Errorf("invalid send subject accepted: %q", invalid)
		}
	}
	for _, invalid := range []string{registration + ".extra", strings.Replace(registration, ".register", ".send", 1), strings.Replace(registration, "discord-main", "bad.key", 1)} {
		if _, parseErr := natswire.ParseNotificationRegistrationSubject(invalid); parseErr == nil {
			t.Errorf("invalid registration subject accepted: %q", invalid)
		}
	}
}
