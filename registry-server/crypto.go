package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
)

// sha256Sum returns the SHA256 hash of data.
func sha256Sum(data []byte) [32]byte {
	return sha256.Sum256(data)
}

// generateRandomKey generates a cryptographically random hex string.
func generateRandomKey(length int) string {
	b := make([]byte, length)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
