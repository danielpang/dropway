package sandbox

import (
	"crypto/rand"
	"encoding/hex"
)

// RandomToken returns a 256-bit hex bearer token for authenticating the API to
// a sandbox's in-container agent. It is generated per sandbox and injected at
// create time; it is the only credential the sandbox ever holds.
func RandomToken() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure is catastrophic and unrecoverable; a caller cannot
		// meaningfully proceed without a token, so panic rather than return a
		// weak/empty one.
		panic("sandbox: crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}
