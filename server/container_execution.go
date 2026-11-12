package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/Philocyber/counterweight/execution"
)

func (e *Engine) executionReady(mode string) error {
	if e.execution == nil {
		return errors.New("Execution isolation self-test has not passed")
	}
	s := e.execution.ModeStatus(mode)
	if s.Status != "ready" {
		if s.Reason != "" {
			return errors.New(s.Reason)
		}
		return errors.New("Execution isolation self-test has not passed")
	}
	if mode == execution.Container {
		d := e.execution.Docker()
		if d == nil {
			return errors.New("Container backend is unavailable")
		}
		return d.CheckAvailable(context.Background())
	}
	return nil
}

func parseIsolationSetting(raw json.RawMessage) (*string, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		var on bool
		if err = json.Unmarshal(raw, &on); err != nil {
			return nil, errors.New("Isolation must be a boolean or off, confined-files, container")
		}
		value = "off"
		if on {
			value = execution.ConfinedFiles
		}
	}
	mode, err := execution.ParseMode(value)
	if err != nil {
		return nil, err
	}
	return &mode, nil
}

func persistedIsolationMode(mode string) string {
	if mode == execution.Container {
		return mode
	}
	if mode == "off" {
		return "false"
	}
	return "true"
}
