// Package august translates explicitly selected US August locks into Hearth Entities.
package august

import (
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/mholtzscher/hearth/entitytypes/lockv1"
	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"
)

const (
	supportedModel = "AUG-SL05-M01-G01"
	lockIDLength   = 32
	keyLock        = "lock"
	keyDoorOpen    = "door-open"
	keyBattery     = "battery"
	roleSuperuser  = "superuser"
)

// Config describes one stateless account connection and its explicit bindings.
type Config struct {
	InstallationID    string
	PollInterval      time.Duration
	InventoryInterval time.Duration
	Bindings          []BindingConfig
}

// BindingConfig selects a lock by identity, never its display name.
type BindingConfig struct {
	BindingKey      string
	LockID          string
	Name            string
	CommandsEnabled bool
}

func (c Config) Validate() error {
	if id, err := uuid.Parse(c.InstallationID); err != nil || id == uuid.Nil {
		return errors.New("installation_id must be a nonzero UUID")
	}
	if c.PollInterval < time.Minute || c.PollInterval > time.Hour {
		return errors.New("poll_interval must be between 1m and 1h")
	}
	if c.InventoryInterval < c.PollInterval || c.InventoryInterval > time.Hour {
		return errors.New("inventory_interval must be between poll_interval and 1h")
	}
	if len(c.Bindings) == 0 || len(c.Bindings) > 64 {
		return errors.New("locks must contain between 1 and 64 bindings")
	}
	keys, ids := make(map[string]bool), make(map[lockID]bool)
	for _, binding := range c.Bindings {
		if err := platformconfig.ValidateSlug("binding_key", binding.BindingKey); err != nil {
			return errors.New("binding_key must be a subject-safe slug")
		}
		id, err := parseLockID(binding.LockID)
		if err != nil {
			return err
		}
		if keys[binding.BindingKey] || ids[id] {
			return errors.New("lock binding keys and lock IDs must be unique")
		}
		if !utf8.ValidString(binding.Name) || utf8.RuneCountInString(binding.Name) > 128 ||
			(binding.Name != "" && strings.TrimSpace(binding.Name) == "") {
			return errors.New("lock name must be valid UTF-8 with at most 128 characters")
		}
		keys[binding.BindingKey], ids[id] = true, true
	}
	return nil
}

// Credentials are startup input. They must never be logged or persisted by the Adapter.
type Credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (c Credentials) Validate() error {
	if strings.TrimSpace(c.Email) == "" || !strings.Contains(c.Email, "@") || c.Password == "" {
		return errors.New("credentials require an email and nonempty password")
	}
	return nil
}

type lockID string

func parseLockID(value string) (lockID, error) {
	if len(value) != lockIDLength {
		return "", errors.New("lock_id must contain exactly 32 hexadecimal characters")
	}
	if _, err := hex.DecodeString(value); err != nil {
		return "", errors.New("lock_id must contain exactly 32 hexadecimal characters")
	}
	return lockID(strings.ToUpper(value)), nil
}

type inventoryEntry struct {
	ID       lockID
	Name     string
	UserType string
}

type device struct {
	ID       lockID
	Name     string
	Model    string
	Firmware string
	UserType string
}

type reading[T any] struct {
	Value           T
	SourceUpdatedAt *time.Time
}

type report struct {
	ReceivedAt time.Time
	Online     *bool
	Lock       *reading[lockv1.State]
	DoorOpen   *reading[bool]
	Battery    *reading[float64]
}

type deviceDetail struct {
	Device  device
	Report  report
	Channel string
}
