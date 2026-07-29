// Package maintenance schedules non-blocking skill and CLI release checks.
package maintenance

import (
	"encoding/json"
	"errors"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"time"
)

const (
	stateFileName = "maintenance.json"
	stateVersion  = 1
	CheckInterval = 6 * time.Hour
)

type State struct {
	Version             int    `json:"version"`
	LastCheckedAt       string `json:"last_checked_at,omitempty"`
	NextCheckAt         string `json:"next_check_at,omitempty"`
	InstalledCLIVersion string `json:"installed_cli_version,omitempty"`
	LatestCLIVersion    string `json:"latest_cli_version,omitempty"`
	UpdateAvailable     bool   `json:"update_available"`
	LastNoticeAt        string `json:"last_notice_at,omitempty"`
}

func Read(configDir string) (State, error) {
	file, err := os.Open(filepath.Join(configDir, stateFileName))
	if errors.Is(err, os.ErrNotExist) {
		return State{Version: stateVersion}, nil
	}
	if err != nil {
		return State{}, err
	}
	defer file.Close()
	var state State
	decoder := json.NewDecoder(io.LimitReader(file, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return State{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) ||
		state.Version != stateVersion {
		return State{}, errors.New("maintenance state is invalid")
	}
	return state, nil
}

func Write(configDir string, state State) error {
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return err
	}
	state.Version = stateVersion
	encoded, err := json.Marshal(state)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	temporary, err := os.CreateTemp(configDir, ".kado-maintenance-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	keep := false
	defer func() {
		_ = temporary.Close()
		if !keep {
			_ = os.Remove(name)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temporary.Write(encoded); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, filepath.Join(configDir, stateFileName)); err != nil {
		return err
	}
	keep = true
	return nil
}

func Due(state State, now time.Time) bool {
	next, err := time.Parse(time.RFC3339, state.NextCheckAt)
	return err != nil || !now.Before(next)
}

func Complete(
	state State,
	now time.Time,
	installed, latest string,
	updateAvailable bool,
) State {
	jitter := time.Duration(rand.IntN(31)-15) * time.Minute
	state.Version = stateVersion
	state.LastCheckedAt = now.UTC().Format(time.RFC3339)
	state.NextCheckAt = now.Add(CheckInterval + jitter).UTC().Format(time.RFC3339)
	state.InstalledCLIVersion = installed
	state.LatestCLIVersion = latest
	state.UpdateAvailable = updateAvailable
	return state
}

func NoticeDue(state State, now time.Time) bool {
	if !state.UpdateAvailable || state.LatestCLIVersion == "" {
		return false
	}
	last, err := time.Parse(time.RFC3339, state.LastNoticeAt)
	return err != nil || now.Sub(last) >= 24*time.Hour
}
