package esphome

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi"
)

const (
	macBytes = 6
	// entityKeySlugBytes bounds the readable part of an Entity key; the hash suffix keeps keys unique.
	entityKeySlugBytes = 40
	entityKeyHashHex   = 8
	maximumNameRunes   = 128
	// eligibleDeviceID is the main device; v1 skips sub-devices.
	eligibleDeviceID = 0
)

// MAC is a device's network MAC address, the Device identity.
type MAC [macBytes]byte

// ParseMAC accepts six hexadecimal octets separated by colons or dashes, or twelve hexadecimal digits, in any case.
func ParseMAC(text string) (MAC, error) {
	compact := strings.ToLower(strings.NewReplacer(":", "", "-", "").Replace(strings.TrimSpace(text)))
	separated := strings.ContainsAny(text, ":-")
	if separated && strings.Count(text, ":")+strings.Count(text, "-") != macBytes-1 {
		return MAC{}, errors.New("MAC address must have six octets")
	}
	decoded, err := hex.DecodeString(compact)
	if err != nil || len(decoded) != macBytes {
		return MAC{}, errors.New("MAC address must be six hexadecimal octets")
	}
	var mac MAC
	copy(mac[:], decoded)
	return mac, nil
}

// String is the lowercase colon form, which is also the Device external ID.
func (mac MAC) String() string {
	parts := make([]string, macBytes)
	for index, octet := range mac {
		parts[index] = fmt.Sprintf("%02x", octet)
	}
	return strings.Join(parts, ":")
}

// BindingKey is the Adapter-scoped Binding key for the device.
func (mac MAC) BindingKey() string { return "esphome-" + hex.EncodeToString(mac[:]) }

// EntityRef is the operator-facing override key "<type>/<name>".
type EntityRef struct {
	Type nativeapi.EntityType
	Name string
}

// ParseEntityRef parses "<type>/<name>". The type must be one the Adapter maps.
func ParseEntityRef(text string) (EntityRef, error) {
	platform, name, found := strings.Cut(text, "/")
	ref := EntityRef{Type: nativeapi.EntityType(platform), Name: name}
	switch ref.Type {
	case nativeapi.EntitySensor,
		nativeapi.EntityTextSensor,
		nativeapi.EntityBinarySensor,
		nativeapi.EntitySwitch,
		nativeapi.EntityLight,
		nativeapi.EntityCover,
		nativeapi.EntityLock:
	case nativeapi.EntityOther:
		fallthrough
	default:
		return EntityRef{}, errors.New(
			"entity reference type must be sensor, text_sensor, binary_sensor, switch, light, cover, or lock",
		)
	}
	if !found || name == "" || utf8.RuneCountInString(name) > maximumNameRunes {
		return EntityRef{}, errors.New("entity reference must be <type>/<name> with a name of 1 to 128 characters")
	}
	return ref, nil
}

func (ref EntityRef) String() string { return string(ref.Type) + "/" + ref.Name }

// Exposure holds a device's include and exclude overrides.
type Exposure struct {
	Include map[EntityRef]struct{}
	Exclude map[EntityRef]struct{}
}

// upstreamRef identifies one ESPHome entity on one connection.
type upstreamRef struct {
	Type     nativeapi.EntityType
	DeviceID uint32
	Key      uint32
}

// upstreamIdentity is Home Assistant's version-3 unique ID without the MAC: "<device_id>/<type>/<name>".
func upstreamIdentity(info nativeapi.EntityInfo) string {
	return fmt.Sprintf("%d/%s/%s", info.DeviceID, info.Type, info.Name)
}

// entityKey retains the original MAC-less hash input so existing Binding-local mappings survive external-ID scoping.
func entityKey(name, identity string) string {
	slug := slugify(name)
	if len(slug) > entityKeySlugBytes {
		slug = strings.TrimRight(slug[:entityKeySlugBytes], "-")
	}
	if slug == "" {
		slug = "entity"
	}
	sum := sha256.Sum256([]byte(identity))
	return slug + "-" + hex.EncodeToString(sum[:])[:entityKeyHashHex]
}

// slugify lowercases name and replaces each run of characters outside [a-z0-9] with one "-".
func slugify(name string) string {
	var builder strings.Builder
	pendingDash := false
	for _, character := range strings.ToLower(name) {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			if pendingDash && builder.Len() > 0 {
				builder.WriteByte('-')
			}
			pendingDash = false
			builder.WriteRune(character)
			continue
		}
		pendingDash = true
	}
	return builder.String()
}

// truncateName bounds a display name to the registration limit.
func truncateName(name string) string {
	if utf8.RuneCountInString(name) <= maximumNameRunes {
		return name
	}
	return string([]rune(name)[:maximumNameRunes])
}
