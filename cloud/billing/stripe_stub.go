//go:build cloud

package billing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
)

// StubSignatureVerifier is a DB/Stripe-free SignatureVerifier used to compile and
// unit-test the webhook handler. It models the SHAPE of Stripe's scheme — an
// HMAC-SHA256 of the payload keyed by the webhook secret, compared in constant
// time — but the production cloud build replaces it with
// stripe-go's webhook.ConstructEvent (timestamp tolerance, v1 signature list,
// replay protection). The payload is expected to be the JSON of an Event.
//
// This is deliberately a skeleton: it proves the handler's contract (verify →
// parse → Event) without taking a hard dependency on stripe-go.
type StubSignatureVerifier struct {
	Secret string
}

// ErrBadSignature is returned when the computed HMAC doesn't match the header.
var ErrBadSignature = errors.New("billing: signature mismatch")

// Verify recomputes HMAC-SHA256(payload, secret) and constant-time-compares it to
// sigHeader (hex). On match it unmarshals the payload into an Event.
func (s StubSignatureVerifier) Verify(payload []byte, sigHeader string) (Event, error) {
	mac := hmac.New(sha256.New, []byte(s.Secret))
	mac.Write(payload)
	want := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(want), []byte(sigHeader)) {
		return Event{}, ErrBadSignature
	}

	var ev Event
	if err := json.Unmarshal(payload, &ev); err != nil {
		return Event{}, err
	}
	if ev.ID == "" {
		return Event{}, errors.New("billing: event missing id")
	}
	return ev, nil
}

// Sign is a test/helper that produces the hex signature for a payload, mirroring
// what Stripe's CLI would send. Not used in production.
func Sign(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}
