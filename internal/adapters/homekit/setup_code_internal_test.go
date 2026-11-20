package homekit

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

func TestSetupURIKnownVectorAndRoundTrip(t *testing.T) {
	t.Parallel()
	// Published HOOBS configuration: https://pastebin.com/jeRtNGVW
	identity := SetupIdentity{Code: "03145154", ID: "LD96"}
	uri, err := identity.setupURI()
	if err != nil {
		t.Fatal(err)
	}
	if uri != "X-HM://0023ISYWYLD96" {
		t.Fatalf("URI = %q", uri)
	}
	check := func(identity SetupIdentity) {
		t.Helper()
		encoded, setupErr := identity.setupURI()
		if setupErr != nil {
			t.Fatal(setupErr)
		}
		value, parseErr := strconv.ParseUint(encoded[7:16], 36, 64)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		if got := fmt.Sprintf("%08d", value&((1<<27)-1)); got != identity.Code {
			t.Fatalf("code = %q, want %q", got, identity.Code)
		}
		if category := (value >> 31) & 0xff; category != 2 {
			t.Fatalf("category = %d", category)
		}
		if flags := (value >> 27) & 0xf; flags != 2 || value>>39 != 0 {
			t.Fatalf("flags = %d or reserved bits set", flags)
		}
		if encoded[16:] != identity.ID {
			t.Fatal("setup ID differs")
		}
	}
	check(identity)
	check(SetupIdentity{Code: "00102003", ID: "0AZ9"})
}

func TestSetupIdentityGenerationAndValidation(t *testing.T) {
	t.Parallel()
	for _, code := range []string{
		"00000000", "11111111", "22222222", "33333333", "44444444",
		"55555555", "66666666", "77777777", "88888888", "99999999",
		"12345678", "87654321", "123-45-678", "abcdefgh", "1234567",
	} {
		if validSetupCode(code) {
			t.Errorf("accepted invalid code %q", code)
		}
	}
	for range 100 {
		identity, err := newSetupIdentity()
		if err != nil {
			t.Fatal(err)
		}
		if !identity.valid() {
			t.Fatal("generated invalid identity")
		}
	}
	for _, identity := range []SetupIdentity{
		{Code: "12345678", ID: "ABCD"},
		{Code: "00102003", ID: "abcD"},
		{Code: "00102003", ID: strings.Repeat("A", 5)},
	} {
		if _, err := identity.setupURI(); err == nil {
			t.Fatal("accepted invalid identity")
		}
	}
}
