package natswire

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	subjectPrefix         = "hearth.v1.adapter"
	runtimeBaseTokenCount = 7
	commandTrailingTokens = 2

	deviceFactSubjectPrefix = "hearth.v1.core.fact"
	deviceFactEntityScope   = "entity"
	deviceFactTokenCount    = 8
)

var (
	slugPattern      = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	runtimeIDPattern = regexp.MustCompile(`^run_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	entityIDPattern  = regexp.MustCompile(`^ent_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

type AdapterClaimRoute struct {
	AdapterID string
}

type AdapterHeartbeatRoute struct {
	AdapterID string
	RuntimeID string
}

type AdapterReleaseRoute struct {
	AdapterID string
	RuntimeID string
}

type RegistrationRoute struct {
	AdapterID string
	RuntimeID string
}

type OwnedMappingsRoute struct {
	AdapterID string
	RuntimeID string
}

type EntityAvailabilityRoute struct {
	AdapterID string
	RuntimeID string
}

type ObservationRoute struct {
	AdapterID string
	RuntimeID string
	EntityID  string
}

type EntityEventRoute struct {
	AdapterID string
	RuntimeID string
	EntityID  string
}

type CommandRoute struct {
	AdapterID     string
	RuntimeID     string
	EntityID      string
	OperationName string
}

type EntityEnablementRoute struct {
	AdapterID string
	RuntimeID string
	EntityID  string
}

// DeviceFactFamily is the closed family token of a Device Fact subject:
// hearth.v1.core.fact.entity.<entity_id>.<family>.<variant>.
type DeviceFactFamily string

const (
	DeviceFactFamilyObservation DeviceFactFamily = "observation"
	DeviceFactFamilyEntityEvent DeviceFactFamily = "entity-event"
)

// DeviceFact variants. These closed vocabularies mirror the strict fact
// schemas and the devices domain values they are built from.
const (
	ObservationFactApplied   = "applied"
	ObservationFactUnchanged = "unchanged"
)

// DeviceFactRoute is the routing identity carried by one concrete Device Fact
// subject: the canonical Entity, the fact family and the family-specific
// variant. Subscribers must reject a payload whose Entity, family and variant
// disagree with the subject they received it on.
type DeviceFactRoute struct {
	EntityID string
	Family   DeviceFactFamily
	Variant  string
}

func AdapterClaimWildcard() string {
	return subjectPrefix + ".*.claim"
}

func AdapterClaimSubject(adapterID string) (string, error) {
	if err := validateSlug("adapter ID", adapterID); err != nil {
		return "", err
	}
	return subjectPrefix + "." + adapterID + ".claim", nil
}

func AdapterHeartbeatWildcard() string {
	return runtimeWildcard("heartbeat")
}

func AdapterHeartbeatSubject(adapterID, runtimeID string) (string, error) {
	return runtimeSubject(adapterID, runtimeID, "heartbeat")
}

func AdapterReleaseWildcard() string {
	return runtimeWildcard("release")
}

func AdapterReleaseSubject(adapterID, runtimeID string) (string, error) {
	return runtimeSubject(adapterID, runtimeID, "release")
}

func RegistrationWildcard() string {
	return runtimeWildcard("register")
}

func RegistrationSubject(adapterID, runtimeID string) (string, error) {
	return runtimeSubject(adapterID, runtimeID, "register")
}

func OwnedMappingsWildcard() string {
	return runtimeWildcard("mappings")
}

func OwnedMappingsSubject(adapterID, runtimeID string) (string, error) {
	return runtimeSubject(adapterID, runtimeID, "mappings")
}

func EntityAvailabilityWildcard() string {
	return runtimeWildcard("availability")
}

func EntityAvailabilitySubject(adapterID, runtimeID string) (string, error) {
	return runtimeSubject(adapterID, runtimeID, "availability")
}

func ObservationWildcard() string {
	return runtimeWildcard("observation") + ".*"
}

func ObservationSubject(adapterID, runtimeID, entityID string) (string, error) {
	base, err := runtimeSubject(adapterID, runtimeID, "observation")
	if err != nil {
		return "", err
	}
	if validationErr := validateEntityID(entityID); validationErr != nil {
		return "", validationErr
	}
	return base + "." + entityID, nil
}

func EntityEnablementWildcard() string {
	return runtimeWildcard("enablement") + ".*"
}

func EntityEnablementSubject(adapterID, runtimeID, entityID string) (string, error) {
	base, err := runtimeSubject(adapterID, runtimeID, "enablement")
	if err != nil {
		return "", err
	}
	if validationErr := validateEntityID(entityID); validationErr != nil {
		return "", validationErr
	}
	return base + "." + entityID, nil
}

// EntityEventWildcard is the Adapter-originated entity event route: one
// occurrence report for one Entity, scoped to the publishing Adapter runtime.
func EntityEventWildcard() string {
	return runtimeWildcard("entity-event") + ".*"
}

func EntityEventSubject(adapterID, runtimeID, entityID string) (string, error) {
	base, err := runtimeSubject(adapterID, runtimeID, "entity-event")
	if err != nil {
		return "", err
	}
	if validationErr := validateEntityID(entityID); validationErr != nil {
		return "", validationErr
	}
	return base + "." + entityID, nil
}

func CommandSubject(adapterID, runtimeID, entityID, operationName string) (string, error) {
	base, err := runtimeSubject(adapterID, runtimeID, "command")
	if err != nil {
		return "", err
	}
	if validationErr := validateEntityID(entityID); validationErr != nil {
		return "", validationErr
	}
	if validationErr := validateSlug("operation name", operationName); validationErr != nil {
		return "", validationErr
	}
	return base + "." + entityID + "." + operationName, nil
}

func CommandWildcard(adapterID, runtimeID string) (string, error) {
	base, err := runtimeSubject(adapterID, runtimeID, "command")
	if err != nil {
		return "", err
	}
	return base + ".*.*", nil
}

// DeviceFactWildcard is the subscription pattern for every Device Fact.
func DeviceFactWildcard() string {
	return deviceFactSubjectPrefix + ".>"
}

// EntityDeviceFactsWildcard is the subscription pattern for every Device Fact
// of one Entity, across all families and variants.
func EntityDeviceFactsWildcard(entityID string) (string, error) {
	if err := validateEntityID(entityID); err != nil {
		return "", err
	}
	return deviceFactEntityPrefix(entityID) + ".>", nil
}

// DeviceFactFamilyWildcard is the subscription pattern for every variant of
// one Device Fact family across all Entities.
func DeviceFactFamilyWildcard(family DeviceFactFamily) (string, error) {
	if err := validateDeviceFactFamily(family); err != nil {
		return "", err
	}
	return deviceFactSubjectPrefix + "." + deviceFactEntityScope + ".*." + string(family) + ".>", nil
}

// ObservationFactSubject builds the subject for one accepted Observation fact.
// The disposition must be applied or unchanged; rejected and duplicate
// Observations are not facts.
func ObservationFactSubject(entityID, disposition string) (string, error) {
	return deviceFactSubject(entityID, DeviceFactFamilyObservation, disposition)
}

// EntityEventFactSubject builds the subject for one first-seen accepted Entity
// Event fact. The name must be a canonical event-name slug.
func EntityEventFactSubject(entityID, name string) (string, error) {
	return deviceFactSubject(entityID, DeviceFactFamilyEntityEvent, name)
}

// ParseDeviceFactSubject parses one concrete Device Fact subject. Wildcards,
// unknown families and variants, noncanonical Entity IDs and any subject whose
// tokens do not round-trip are rejected. Callers must compare the returned
// route with the decoded payload and reject disagreement.
func ParseDeviceFactSubject(subject string) (DeviceFactRoute, error) {
	parts := strings.Split(subject, ".")
	if len(parts) != deviceFactTokenCount || strings.Join(parts[:4], ".") != deviceFactSubjectPrefix ||
		parts[4] != deviceFactEntityScope {
		return DeviceFactRoute{}, fmt.Errorf("invalid Device Fact subject %q", subject)
	}
	route := DeviceFactRoute{EntityID: parts[5], Family: DeviceFactFamily(parts[6]), Variant: parts[7]}
	canonical, err := deviceFactSubject(route.EntityID, route.Family, route.Variant)
	if err != nil {
		return DeviceFactRoute{}, fmt.Errorf("invalid Device Fact subject %q: %w", subject, err)
	}
	if canonical != subject {
		return DeviceFactRoute{}, fmt.Errorf("invalid Device Fact subject %q: subject is not canonical", subject)
	}
	return route, nil
}

func deviceFactEntityPrefix(entityID string) string {
	return deviceFactSubjectPrefix + "." + deviceFactEntityScope + "." + entityID
}

func deviceFactSubject(entityID string, family DeviceFactFamily, variant string) (string, error) {
	if err := validateEntityID(entityID); err != nil {
		return "", err
	}
	if err := validateDeviceFactFamily(family); err != nil {
		return "", err
	}
	if err := validateDeviceFactVariant(family, variant); err != nil {
		return "", err
	}
	return deviceFactEntityPrefix(entityID) + "." + string(family) + "." + variant, nil
}

func validateDeviceFactFamily(family DeviceFactFamily) error {
	switch family {
	case DeviceFactFamilyObservation, DeviceFactFamilyEntityEvent:
		return nil
	}
	return fmt.Errorf("invalid Device Fact family %q", family)
}

func validateDeviceFactVariant(family DeviceFactFamily, variant string) error {
	switch family {
	case DeviceFactFamilyObservation:
		if variant != ObservationFactApplied && variant != ObservationFactUnchanged {
			return fmt.Errorf("invalid Observation fact disposition %q", variant)
		}
	case DeviceFactFamilyEntityEvent:
		if !slugPattern.MatchString(variant) {
			return fmt.Errorf("invalid Entity Event fact name %q", variant)
		}
	}
	return nil
}

func AllCommandsWildcard() string {
	return runtimeWildcard("command") + ".*.*"
}

func ParseAdapterClaimSubject(subject string) (AdapterClaimRoute, error) {
	parts := strings.Split(subject, ".")
	if len(parts) != 5 || strings.Join(parts[:3], ".") != subjectPrefix || parts[4] != "claim" {
		return AdapterClaimRoute{}, fmt.Errorf("invalid Adapter claim subject %q", subject)
	}
	if _, err := AdapterClaimSubject(parts[3]); err != nil {
		return AdapterClaimRoute{}, fmt.Errorf("invalid Adapter claim subject %q: %w", subject, err)
	}
	return AdapterClaimRoute{AdapterID: parts[3]}, nil
}

func ParseAdapterHeartbeatSubject(subject string) (AdapterHeartbeatRoute, error) {
	adapterID, runtimeID, err := parseRuntimeSubject(subject, "heartbeat")
	if err != nil {
		return AdapterHeartbeatRoute{}, fmt.Errorf("invalid Adapter heartbeat subject %q: %w", subject, err)
	}
	return AdapterHeartbeatRoute{AdapterID: adapterID, RuntimeID: runtimeID}, nil
}

func ParseAdapterReleaseSubject(subject string) (AdapterReleaseRoute, error) {
	adapterID, runtimeID, err := parseRuntimeSubject(subject, "release")
	if err != nil {
		return AdapterReleaseRoute{}, fmt.Errorf("invalid Adapter release subject %q: %w", subject, err)
	}
	return AdapterReleaseRoute{AdapterID: adapterID, RuntimeID: runtimeID}, nil
}

func ParseRegistrationSubject(subject string) (RegistrationRoute, error) {
	adapterID, runtimeID, err := parseRuntimeSubject(subject, "register")
	if err != nil {
		return RegistrationRoute{}, fmt.Errorf("invalid registration subject %q: %w", subject, err)
	}
	return RegistrationRoute{AdapterID: adapterID, RuntimeID: runtimeID}, nil
}

func ParseOwnedMappingsSubject(subject string) (OwnedMappingsRoute, error) {
	adapterID, runtimeID, err := parseRuntimeSubject(subject, "mappings")
	if err != nil {
		return OwnedMappingsRoute{}, fmt.Errorf("invalid owned mappings subject %q: %w", subject, err)
	}
	return OwnedMappingsRoute{AdapterID: adapterID, RuntimeID: runtimeID}, nil
}

func ParseEntityAvailabilitySubject(subject string) (EntityAvailabilityRoute, error) {
	adapterID, runtimeID, err := parseRuntimeSubject(subject, "availability")
	if err != nil {
		return EntityAvailabilityRoute{}, fmt.Errorf("invalid Entity availability subject %q: %w", subject, err)
	}
	return EntityAvailabilityRoute{AdapterID: adapterID, RuntimeID: runtimeID}, nil
}

func ParseObservationSubject(subject string) (ObservationRoute, error) {
	parts := strings.Split(subject, ".")
	adapterID, runtimeID, err := parseRuntimeSubjectParts(parts, "observation", 1)
	if err != nil {
		return ObservationRoute{}, fmt.Errorf("invalid observation subject %q: %w", subject, err)
	}
	if validationErr := validateEntityID(parts[7]); validationErr != nil {
		return ObservationRoute{}, fmt.Errorf("invalid observation subject %q: %w", subject, validationErr)
	}
	return ObservationRoute{AdapterID: adapterID, RuntimeID: runtimeID, EntityID: parts[7]}, nil
}

func ParseEntityEnablementSubject(subject string) (EntityEnablementRoute, error) {
	parts := strings.Split(subject, ".")
	adapterID, runtimeID, err := parseRuntimeSubjectParts(parts, "enablement", 1)
	if err != nil {
		return EntityEnablementRoute{}, fmt.Errorf("invalid Entity enablement subject %q: %w", subject, err)
	}
	if validationErr := validateEntityID(parts[7]); validationErr != nil {
		return EntityEnablementRoute{}, fmt.Errorf(
			"invalid Entity enablement subject %q: %w", subject, validationErr,
		)
	}
	return EntityEnablementRoute{AdapterID: adapterID, RuntimeID: runtimeID, EntityID: parts[7]}, nil
}

func ParseEntityEventSubject(subject string) (EntityEventRoute, error) {
	parts := strings.Split(subject, ".")
	adapterID, runtimeID, err := parseRuntimeSubjectParts(parts, "entity-event", 1)
	if err != nil {
		return EntityEventRoute{}, fmt.Errorf("invalid entity event subject %q: %w", subject, err)
	}
	if validationErr := validateEntityID(parts[7]); validationErr != nil {
		return EntityEventRoute{}, fmt.Errorf("invalid entity event subject %q: %w", subject, validationErr)
	}
	return EntityEventRoute{AdapterID: adapterID, RuntimeID: runtimeID, EntityID: parts[7]}, nil
}

func ParseCommandSubject(subject string) (CommandRoute, error) {
	parts := strings.Split(subject, ".")
	adapterID, runtimeID, err := parseRuntimeSubjectParts(parts, "command", commandTrailingTokens)
	if err != nil {
		return CommandRoute{}, fmt.Errorf("invalid command subject %q: %w", subject, err)
	}
	if validationErr := validateEntityID(parts[7]); validationErr != nil {
		return CommandRoute{}, fmt.Errorf("invalid command subject %q: %w", subject, validationErr)
	}
	if validationErr := validateSlug("operation name", parts[8]); validationErr != nil {
		return CommandRoute{}, fmt.Errorf("invalid command subject %q: %w", subject, validationErr)
	}
	return CommandRoute{
		AdapterID: adapterID, RuntimeID: runtimeID, EntityID: parts[7], OperationName: parts[8],
	}, nil
}

func runtimeWildcard(operation string) string {
	return subjectPrefix + ".*.runtime.*." + operation
}

func runtimeSubject(adapterID, runtimeID, operation string) (string, error) {
	if err := validateSlug("adapter ID", adapterID); err != nil {
		return "", err
	}
	if err := validateRuntimeID(runtimeID); err != nil {
		return "", err
	}
	return subjectPrefix + "." + adapterID + ".runtime." + runtimeID + "." + operation, nil
}

func parseRuntimeSubject(subject, operation string) (string, string, error) {
	return parseRuntimeSubjectParts(strings.Split(subject, "."), operation, 0)
}

func parseRuntimeSubjectParts(parts []string, operation string, trailingTokens int) (string, string, error) {
	wantTokenCount := runtimeBaseTokenCount + trailingTokens
	if len(parts) != wantTokenCount || strings.Join(parts[:3], ".") != subjectPrefix ||
		parts[4] != "runtime" || parts[6] != operation {
		return "", "", errorsForTokenCount(operation, wantTokenCount, len(parts))
	}
	if err := validateSlug("adapter ID", parts[3]); err != nil {
		return "", "", err
	}
	if err := validateRuntimeID(parts[5]); err != nil {
		return "", "", err
	}
	return parts[3], parts[5], nil
}

func errorsForTokenCount(route string, want, got int) error {
	return fmt.Errorf("invalid %s route: got %d route tokens, want %d", route, got, want)
}

func validateSlug(name, value string) error {
	if !slugPattern.MatchString(value) {
		return fmt.Errorf("invalid %s %q", name, value)
	}
	return nil
}

func validateRuntimeID(value string) error {
	if !runtimeIDPattern.MatchString(value) {
		return fmt.Errorf("invalid runtime ID %q", value)
	}
	return nil
}

func validateEntityID(value string) error {
	if !entityIDPattern.MatchString(value) {
		return fmt.Errorf("invalid entity ID %q", value)
	}
	return nil
}
