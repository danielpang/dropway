// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package auth implements the CLI's browser sign-in: `dropway login` runs an
// OAuth 2.1 loopback flow (PKCE + dynamic client registration) against the Dropway
// authorization server, stores the resulting credentials locally, and refreshes
// the access token as needed so `dropway deploy` just works. DROPWAY_TOKEN still
// takes precedence for CI / non-interactive use.
package auth

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Credentials are what `dropway login` persists. The refresh token lets the CLI
// mint fresh access tokens without re-opening the browser; tokenURL/clientID/
// resource are kept so a refresh can be performed offline from this file alone.
type Credentials struct {
	APIBase      string    `json:"api_base"`
	TokenURL     string    `json:"token_url"`
	ClientID     string    `json:"client_id"`
	Resource     string    `json:"resource"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	Expiry       time.Time `json:"expiry"`
}

// expired reports whether the access token is at/near expiry (30s of slack so a
// token doesn't lapse mid-request).
func (c *Credentials) expired() bool {
	if c.Expiry.IsZero() {
		return true
	}
	return !time.Now().Add(30 * time.Second).Before(c.Expiry)
}

// CredentialsPath is where credentials are stored: $XDG_CONFIG_HOME/dropway/
// credentials.json, or ~/.config/dropway/credentials.json. Honoring XDG also makes
// it overridable in tests.
func CredentialsPath() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "dropway", "credentials.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "dropway", "credentials.json"), nil
}

// Load reads stored credentials. Returns os.ErrNotExist when there are none.
func Load() (*Credentials, error) {
	path, err := CredentialsPath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err // includes os.ErrNotExist
	}
	var c Credentials
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// Save writes credentials with owner-only permissions (the file holds tokens).
func Save(c *Credentials) error {
	path, err := CredentialsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// Delete removes the stored credentials (no error if there are none).
func Delete() error {
	path, err := CredentialsPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
