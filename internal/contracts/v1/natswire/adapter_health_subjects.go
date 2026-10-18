package natswire

import (
	"errors"
	"strings"
)

const adapterHealthPrefix = "hearth.v1.core.adapter-health."

type AdapterHealthRoute struct{ AdapterID string }

func AdapterHealthWildcard() string { return adapterHealthPrefix + "*" }

func AdapterHealthSubject(adapterID string) (string, error) {
	if !slugPattern.MatchString(adapterID) {
		return "", errors.New("invalid Adapter health route")
	}
	return adapterHealthPrefix + adapterID, nil
}

func ParseAdapterHealthSubject(subject string) (AdapterHealthRoute, error) {
	id, ok := strings.CutPrefix(subject, adapterHealthPrefix)
	if !ok || !slugPattern.MatchString(id) {
		return AdapterHealthRoute{}, errors.New("invalid Adapter health route")
	}
	return AdapterHealthRoute{AdapterID: id}, nil
}
