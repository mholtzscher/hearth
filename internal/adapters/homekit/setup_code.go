package homekit

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

type SetupIdentity struct {
	Code string `json:"code"`
	ID   string `json:"id"`
}

var errInvalidSetup = errors.New("invalid HomeKit setup identity")

const (
	setupCodeLimit  = 100000000
	setupCodeLength = 8
	setupIDLength   = 4
	setupPayloadLen = 9
	bridgeCategory  = 2
)

func newSetupIdentity() (SetupIdentity, error) {
	var identity SetupIdentity
	for {
		n, err := rand.Int(rand.Reader, big.NewInt(setupCodeLimit))
		if err != nil {
			return SetupIdentity{}, fmt.Errorf("generate setup code: %w", err)
		}
		identity.Code = fmt.Sprintf("%08d", n.Int64())
		if validSetupCode(identity.Code) {
			break
		}
	}
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	id := make([]byte, setupIDLength)
	for i := range id {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return SetupIdentity{}, fmt.Errorf("generate setup ID: %w", err)
		}
		id[i] = alphabet[n.Int64()]
	}
	identity.ID = string(id)
	return identity, nil
}

func validSetupCode(code string) bool {
	if len(code) != setupCodeLength {
		return false
	}
	for i := range len(code) {
		if code[i] < '0' || code[i] > '9' {
			return false
		}
	}
	if code == "12345678" || code == "87654321" {
		return false
	}
	return strings.Trim(code, code[:1]) != ""
}

func (s SetupIdentity) valid() bool {
	if !validSetupCode(s.Code) || len(s.ID) != setupIDLength {
		return false
	}
	for i := range len(s.ID) {
		if !strings.ContainsRune("0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ", rune(s.ID[i])) {
			return false
		}
	}
	return true
}

// setupURI encodes the bridge category (2), IP transport and unpaired PIN.
func (s SetupIdentity) setupURI() (string, error) {
	if !s.valid() {
		return "", errInvalidSetup
	}
	pin, err := strconv.ParseUint(s.Code, 10, 32)
	if err != nil {
		return "", errInvalidSetup
	}
	payload := uint64(bridgeCategory)<<31 | uint64(1)<<28 | pin
	encoded := strings.ToUpper(strconv.FormatUint(payload, 36))
	return "X-HM://" + strings.Repeat("0", setupPayloadLen-len(encoded)) + encoded + s.ID, nil
}
