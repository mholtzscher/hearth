package esphome //nolint:testpackage // Key derivation is unexported.

import (
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi"
)

func TestParseMAC(t *testing.T) {
	t.Parallel()
	for _, text := range []string{"02:00:00:00:00:A1", "02-00-00-00-00-a1", "0200000000a1", " 02:00:00:00:00:a1 "} {
		mac, err := ParseMAC(text)
		if err != nil {
			t.Errorf("ParseMAC(%q): %v", text, err)
			continue
		}
		if mac.String() != "02:00:00:00:00:a1" || mac.BindingKey() != "esphome-0200000000a1" {
			t.Errorf("ParseMAC(%q) = %s %s", text, mac, mac.BindingKey())
		}
	}
	for _, text := range []string{"", "02:00:00:00:00", "02:00:00:00:00:a1:ff", "zz:00:00:00:00:a1", "0200:00:00:00:a1"} {
		if _, err := ParseMAC(text); err == nil {
			t.Errorf("ParseMAC(%q) succeeded", text)
		}
	}
}

func TestParseEntityRef(t *testing.T) {
	t.Parallel()
	ref, err := ParseEntityRef("sensor/PM <2.5µm / mass")
	if err != nil || ref != (EntityRef{Type: nativeapi.EntitySensor, Name: "PM <2.5µm / mass"}) {
		t.Fatalf("ref = %+v, %v", ref, err)
	}
	for _, text := range []string{"button/ESP Reboot", "sensor/", "sensor", "/Name", "sensor/" + strings.Repeat("x", 129)} {
		if _, parseEntityRefErr := ParseEntityRef(text); parseEntityRefErr == nil {
			t.Errorf("ParseEntityRef(%q) succeeded", text)
		}
	}
}

func TestEntityKeyShape(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"Temperature Probe":              "temperature-probe-",
		"PM <2.5µm Weight concentration": "pm-2-5-m-weight-concentration-",
		"":                               "entity-",
		"---":                            "entity-",
	}
	for name, prefix := range cases {
		key := entityKey(name, "0/sensor/"+name)
		if !strings.HasPrefix(key, prefix) || !registrationKeyPattern.MatchString(key) {
			t.Errorf("entityKey(%q) = %q", name, key)
		}
	}
	long := entityKey(strings.Repeat("abc ", 30), "0/sensor/long")
	if len(long) > entityKeySlugBytes+1+entityKeyHashHex || strings.Contains(long, "--") {
		t.Errorf("long key = %q", long)
	}
	if entityKey("Lamp", "0/light/Lamp#power") == entityKey("Lamp", "0/light/Lamp#brightness") {
		t.Error("distinct external IDs share a key")
	}
}
