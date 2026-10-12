package natswire

import "strings"

const notificationSubjectTokenCount = runtimeBaseTokenCount + 1

type NotificationRegistrationRoute struct{ AdapterID, RuntimeID string }
type NotificationSendRoute struct{ AdapterID, RuntimeID string }

func NotificationRegistrationWildcard() string {
	return runtimeWildcard("notification-destination.register")
}

func NotificationRegistrationSubject(adapterID, runtimeID string) (string, error) {
	return runtimeSubject(adapterID, runtimeID, "notification-destination.register")
}

func NotificationSendSubject(adapterID, runtimeID string) (string, error) {
	return runtimeSubject(adapterID, runtimeID, "notification.send")
}

func ParseNotificationRegistrationSubject(subject string) (NotificationRegistrationRoute, error) {
	a, r, err := parseNotificationSubject(subject, "notification-destination", "register")
	return NotificationRegistrationRoute{AdapterID: a, RuntimeID: r}, err
}

func ParseNotificationSendSubject(subject string) (NotificationSendRoute, error) {
	a, r, err := parseNotificationSubject(subject, "notification", "send")
	return NotificationSendRoute{AdapterID: a, RuntimeID: r}, err
}

func parseNotificationSubject(subject, family, operation string) (string, string, error) {
	parts := strings.Split(subject, ".")
	a, r, err := parseRuntimeSubjectParts(parts, family, 1)
	if err != nil {
		return "", "", err
	}
	if parts[7] != operation {
		return "", "", errorsForTokenCount(operation, notificationSubjectTokenCount, len(parts))
	}
	return a, r, nil
}
