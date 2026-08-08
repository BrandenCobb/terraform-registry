package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os/exec"
	"strings"
)

// sha256Sum returns the SHA256 hash of data.
func sha256Sum(data []byte) [32]byte {
	return sha256.Sum256(data)
}

// sha256Hex returns the hex-encoded SHA256 hash of data.
func sha256Hex(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

// generateRandomKey generates a cryptographically random hex string.
func generateRandomKey(length int) string {
	b := make([]byte, length)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// VerifyGPG verifies a file's GPG signature against a detached .asc file.
// Returns nil if valid, error if invalid or verification fails.
func VerifyGPG(file, sigFile, keyringPath string) error {
	args := []string{"--verify", "--batch", "--no-tty"}
	if keyringPath != "" {
		args = append(args, "--keyring", keyringPath)
	}
	args = append(args, sigFile, file)

	cmd := exec.Command("gpg", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("GPG verification failed: %s", strings.TrimSpace(string(output)))
	}
	return nil
}

// ArmorGPGKey exports a GPG public key in ASCII armor format by key ID.
func ArmorGPGKey(keyID string) (string, error) {
	cmd := exec.Command("gpg", "--armor", "--export", keyID)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("export GPG key %s: %w", keyID, err)
	}
	return string(output), nil
}

// ImportGPGKey imports a GPG public key from ASCII armor.
func ImportGPGKey(armor string, keyringPath string) error {
	args := []string{"--batch", "--no-tty", "--import"}
	if keyringPath != "" {
		args = append(args, "--keyring", keyringPath)
	}
	cmd := exec.Command("gpg", args...)
	cmd.Stdin = strings.NewReader(armor)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("import GPG key: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}
