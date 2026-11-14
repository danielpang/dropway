// SPDX-License-Identifier: FSL-1.1-Apache-2.0
// Package hosted contains the bounded, provider-independent execution contract.
package hosted

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"regexp"
	"strings"
	"time"
)

var ErrDenied = errors.New("hosted: access denied")
var ErrConflict = errors.New("hosted: execution conflict")
var ErrBudget = errors.New("hosted: budget exhausted")
var ErrInvalid = errors.New("hosted: invalid request")
var ErrUnavailable = errors.New("hosted: execution unavailable")

const MaxFilesBytes = 10 << 20
const MaxTranscriptBytes = 256 << 10
const LeaseDuration = 90 * time.Second
const HarnessVersion = "1.5.0"

type Budget struct {
	MaxTokens      int64 `json:"max_tokens"`
	MaxCalls       int   `json:"max_calls"`
	MaxSeconds     int   `json:"max_seconds"`
	MaxOutputBytes int64 `json:"max_output_bytes"`
}

func (b *Budget) Normalize() error {
	if b.MaxTokens == 0 {
		b.MaxTokens = 100000
	}
	if b.MaxCalls == 0 {
		b.MaxCalls = 20
	}
	if b.MaxSeconds == 0 {
		b.MaxSeconds = 600
	}
	if b.MaxOutputBytes == 0 {
		b.MaxOutputBytes = MaxFilesBytes
	}
	if b.MaxTokens < 1 || b.MaxTokens > 500000 || b.MaxCalls < 1 || b.MaxCalls > 100 || b.MaxSeconds < 1 || b.MaxSeconds > 900 || b.MaxOutputBytes < 1 || b.MaxOutputBytes > MaxFilesBytes {
		return ErrInvalid
	}
	return nil
}

type Config struct {
	Provider       string `json:"provider"`
	ModelID        string `json:"model_id"`
	HarnessID      string `json:"harness_id"`
	HarnessVersion string `json:"harness_version"`
	Prompt         string `json:"prompt"`
	ThreadID       string `json:"thread_id,omitempty"`
	Budget         Budget `json:"budget"`
	ToolPolicy     string `json:"tool_policy"`
}

func (c *Config) Normalize() error {
	c.Prompt = Redact(c.Prompt)
	if c.HarnessID == "" {
		c.HarnessID = "pi"
	}
	if c.HarnessVersion == "" {
		c.HarnessVersion = HarnessVersion
	}
	if c.ToolPolicy == "" {
		c.ToolPolicy = "static-site-v1"
	}
	if c.HarnessID != "pi" || c.HarnessVersion != HarnessVersion || c.ToolPolicy != "static-site-v1" || !ModelAllowed(c.Provider, c.ModelID) || len(c.Prompt) == 0 || len(c.Prompt) > 16000 {
		return ErrInvalid
	}
	return c.Budget.Normalize()
}
func ModelAllowed(provider, model string) bool {
	if provider == "openrouter" {
		if !strings.HasPrefix(model, "anthropic/") {
			return false
		}
		model = strings.TrimPrefix(model, "anthropic/")
	} else if provider != "anthropic" {
		return false
	}
	return model == "claude-sonnet-4-5" || model == "claude-opus-4-1" || model == "claude-haiku-4-5"
}

type Vault struct{ aead cipher.AEAD }

func NewVault(key []byte) (*Vault, error) {
	if len(key) != 32 {
		return nil, ErrUnavailable
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	return &Vault{aead: aead}, err
}
func (v *Vault) Seal(secret, aad string) ([]byte, error) {
	if v == nil || len(secret) < 8 || len(secret) > 4096 {
		return nil, ErrInvalid
	}
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return v.aead.Seal(nonce, nonce, []byte(secret), []byte(aad)), nil
}
func (v *Vault) Open(raw []byte, aad string) (string, error) {
	if v == nil || len(raw) < v.aead.NonceSize() {
		return "", ErrDenied
	}
	n := v.aead.NonceSize()
	plain, err := v.aead.Open(nil, raw[:n], raw[n:], []byte(aad))
	if err != nil {
		return "", ErrDenied
	}
	return string(plain), nil
}
func CredentialAAD(org, owner, id, provider string) string {
	return org + ":" + owner + ":" + id + ":" + provider
}
func Hash(value string) string { h := sha256.Sum256([]byte(value)); return hex.EncodeToString(h[:]) }
func Fingerprint(v any) string { b, _ := json.Marshal(v); return Hash(string(b)) }
func Token(kind, org, attempt string) (string, string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	token := "dw_" + kind + "_" + org + "." + attempt + "." + base64.RawURLEncoding.EncodeToString(b)
	return token, Hash(token), nil
}
func ParseToken(token, kind string) (org, attempt string, err error) {
	rest, ok := strings.CutPrefix(token, "dw_"+kind+"_")
	parts := strings.Split(rest, ".")
	if !ok || len(parts) != 3 || len(parts[0]) != 36 || len(parts[1]) != 36 || len(parts[2]) != 43 {
		return "", "", ErrDenied
	}
	if _, e := uuid.Parse(parts[0]); e != nil {
		return "", "", ErrDenied
	}
	if _, e := uuid.Parse(parts[1]); e != nil {
		return "", "", ErrDenied
	}
	if _, e := base64.RawURLEncoding.DecodeString(parts[2]); e != nil {
		return "", "", ErrDenied
	}
	return parts[0], parts[1], nil
}

type File struct {
	Path          string `json:"path"`
	ContentBase64 string `json:"content_base64"`
	ContentType   string `json:"content_type"`
	SHA256        string `json:"sha256,omitempty"`
	Size          int64  `json:"size,omitempty"`
}

var secretPattern = regexp.MustCompile(`(?:sk-(?:ant-|or-)?[A-Za-z0-9_-]{8,}|dw_(?:gateway|attempt|live)_[A-Za-z0-9_.-]+)`)

func Redact(value string) string { return secretPattern.ReplaceAllString(value, "[redacted]") }
