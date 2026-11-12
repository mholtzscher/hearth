// Package levoit translates Core 400S and 600S VeSync traffic into Hearth Entities.
package levoit

import (
	"errors"
	"strings"
	"time"
)

const (
	modeManual       = "manual"
	modeAuto         = "auto"
	modeSleep        = "sleep"
	maximumPM25      = 1000000
	maximumPercent   = 100
	maximumNameRunes = 128
)

type Config struct {
	PollInterval time.Duration
	TimeZone     string
}

func (c Config) Validate() error {
	if c.PollInterval < 30*time.Second || c.PollInterval > time.Hour {
		return errors.New("poll_interval must be between 30s and 1h")
	}
	if c.TimeZone == "" {
		return errors.New("time_zone is required")
	}
	if _, err := time.LoadLocation(c.TimeZone); err != nil {
		return errors.New("time_zone must be an IANA timezone")
	}
	return nil
}

// Credentials are startup input only and must never be logged or persisted.
type Credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (c Credentials) Validate() error {
	if strings.TrimSpace(c.Email) == "" || c.Password == "" {
		return errors.New("credentials require nonempty email and password")
	}
	return nil
}

type device struct {
	CID          string
	Name         string
	Model        string
	ConfigModule string
	Region       string
	Online       bool
}

type status struct {
	ReceivedAt  time.Time `json:"received_at"`
	Power       *bool     `json:"power"`
	Mode        *string   `json:"mode"`
	ManualLevel *int      `json:"manual_level"`
	Display     *bool     `json:"display"`
	ChildLock   *bool     `json:"child_lock"`
	PM25        *float64  `json:"pm25"`
	FilterLife  *float64  `json:"filter_life"`
}
