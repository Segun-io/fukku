package store

import (
	"crypto/rand"
	"encoding/hex"
)

// NewID returns a new job or upload id: 16 bytes from crypto/rand as 32 lower-case hex
// characters.
func NewID() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // never fails (crypto/rand)
	return hex.EncodeToString(b[:])
}
