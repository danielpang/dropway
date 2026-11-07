// SPDX-License-Identifier: FSL-1.1-Apache-2.0

// Package jobtoken defines scoped, expiring credentials for one orchestration
// attempt and for the model gateway. These tokens are not org API keys and
// cannot approve or publish. Provider API keys are never placed in a token.
package jobtoken

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const (
	prefix = "dw_attempt_"

	AudAttempt = "execution-attempt"
	AudGateway = "model-gateway"

	ScopeSubmit  = "attempt:submit"
	ScopeGateway = "gateway:complete"
	MaxTTL       = 15 * time.Minute
	tokenVersion = 1
)

var (
	ErrDisabled = errors.New("jobtoken: signer not configured")
	ErrInvalid  = errors.New("jobtoken: invalid")
	ErrExpired  = errors.New("jobtoken: expired")
	ErrScope    = errors.New("jobtoken: scope")
	ErrBinding  = errors.New("jobtoken: binding")
)

// Claims is the pinned snapshot frozen into a token. A later settings change
// does not alter a token already issued. There is no field for a provider key
// or a Fly management token.
type Claims struct {
	Version         int      `json:"v"`
	Audience        string   `json:"aud"`
	OrgID           string   `json:"org_id"`
	RoomID          string   `json:"room_id"`
	SiteID          string   `json:"site_id"`
	VersionID       string   `json:"version_id"`
	AttemptID       string   `json:"attempt_id"`
	SessionID       string   `json:"session_id"`
	InitiatorUserID string   `json:"initiator_user_id"`
	CredentialRef   string   `json:"credential_ref"`
	Provider        string   `json:"provider"`
	ModelID         string   `json:"model_id"`
	HarnessID       string   `json:"harness_id"`
	HarnessVersion  string   `json:"harness_version"`
	Scopes          []string `json:"scopes"`
	IssuedAt        int64    `json:"iat"`
	NotBefore       int64    `json:"nbf"`
	ExpiresAt       int64    `json:"exp"`
}

// Binding is what the caller must prove. A token for one attempt, org, or
// candidate cannot satisfy another.
type Binding struct {
	Audience        string
	OrgID           string
	RoomID          string
	SiteID          string
	VersionID       string
	AttemptID       string
	SessionID       string
	InitiatorUserID string
	CredentialRef   string
	Provider        string
	ModelID         string
	HarnessID       string
	HarnessVersion  string
	Scope           string
}

// Signer issues and verifies tokens with an HMAC key that stays in the
// trusted control plane. A nil or empty key refuses every operation.
type Signer struct {
	key []byte
	now func() time.Time
}

func NewSigner(key []byte) (*Signer, error) {
	if len(key) < 32 {
		return nil, ErrDisabled
	}
	k := make([]byte, len(key))
	copy(k, key)
	return &Signer{key: k, now: time.Now}, nil
}

// IssueAttempt mints an execution-attempt token. It does not grant publish
// or approve-change.
func (s *Signer) IssueAttempt(c Claims, ttl time.Duration) (string, error) {
	c.Audience = AudAttempt
	c.Scopes = []string{ScopeSubmit}
	return s.issue(c, ttl)
}

// IssueGateway mints a narrow token the orchestration worker presents to the
// model gateway. It is not a provider key and must not be sent to a Sprite.
func (s *Signer) IssueGateway(c Claims, ttl time.Duration) (string, error) {
	c.Audience = AudGateway
	c.Scopes = []string{ScopeGateway}
	return s.issue(c, ttl)
}

func (s *Signer) issue(c Claims, ttl time.Duration) (string, error) {
	if s == nil || len(s.key) < 32 {
		return "", ErrDisabled
	}
	if ttl <= 0 || ttl > MaxTTL {
		return "", ErrInvalid
	}
	if c.OrgID == "" || c.InitiatorUserID == "" || c.AttemptID == "" || c.CredentialRef == "" {
		return "", ErrInvalid
	}
	if strings.Contains(c.CredentialRef, "sk-") || strings.Contains(strings.ToLower(c.Provider), "key") {
		return "", ErrInvalid
	}
	now := s.now()
	c.Version = tokenVersion
	c.IssuedAt = now.Unix()
	c.NotBefore = now.Unix()
	c.ExpiresAt = now.Add(ttl).Unix()
	return seal(s.key, c)
}

// Verify checks signature, expiry, audience, scope, and the full pinned binding.
func (s *Signer) Verify(token string, bind Binding, now time.Time) (Claims, error) {
	if s == nil || len(s.key) < 32 {
		return Claims{}, ErrDisabled
	}
	c, err := open(s.key, token)
	if err != nil {
		return Claims{}, err
	}
	if now.Unix() < c.NotBefore || now.Unix() >= c.ExpiresAt {
		return Claims{}, ErrExpired
	}
	if c.Audience != bind.Audience || !hasScope(c.Scopes, bind.Scope) {
		return Claims{}, ErrScope
	}
	if c.OrgID != bind.OrgID || c.RoomID != bind.RoomID || c.SiteID != bind.SiteID ||
		c.VersionID != bind.VersionID || c.AttemptID != bind.AttemptID || c.SessionID != bind.SessionID ||
		c.InitiatorUserID != bind.InitiatorUserID || c.CredentialRef != bind.CredentialRef ||
		c.Provider != bind.Provider || c.ModelID != bind.ModelID ||
		c.HarnessID != bind.HarnessID || c.HarnessVersion != bind.HarnessVersion {
		return Claims{}, ErrBinding
	}
	return c, nil
}

func hasScope(scopes []string, want string) bool {
	if want == "" || strings.Contains(want, "*") {
		return false
	}
	for _, s := range scopes {
		if strings.Contains(s, "*") {
			return false
		}
		if s == want {
			return true
		}
	}
	return false
}

func seal(key []byte, c Claims) (string, error) {
	body, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(body)
	sig := mac.Sum(nil)
	return prefix + base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func open(key []byte, token string) (Claims, error) {
	rest, ok := strings.CutPrefix(token, prefix)
	if !ok {
		return Claims{}, ErrInvalid
	}
	payload, sig, ok := strings.Cut(rest, ".")
	if !ok {
		return Claims{}, ErrInvalid
	}
	body, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return Claims{}, ErrInvalid
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return Claims{}, ErrInvalid
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(body)
	if !hmac.Equal(got, mac.Sum(nil)) {
		return Claims{}, ErrInvalid
	}
	var c Claims
	if err := json.Unmarshal(body, &c); err != nil || c.Version != tokenVersion {
		return Claims{}, ErrInvalid
	}
	return c, nil
}
