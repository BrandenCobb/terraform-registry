package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
)

func TestSemanticVersionPrecedence(t *testing.T) {
	versions := []string{"1.0.0", "1.0.0-rc.10", "1.0.0-rc.2", "1.0.0-beta", "1.0.0-alpha"}
	sortSemanticVersions(versions)
	want := []string{"1.0.0-alpha", "1.0.0-beta", "1.0.0-rc.2", "1.0.0-rc.10", "1.0.0"}
	for i := range want {
		if versions[i] != want[i] {
			t.Fatalf("version order = %v, want %v", versions, want)
		}
	}
}

func TestSemanticVersionValidation(t *testing.T) {
	valid := []string{"0.0.0", "1.2.3", "1.2.3-alpha.1", "1.2.3-alpha-beta.1", "1.2.3+build.5"}
	invalid := []string{"v1.2.3", "01.2.3", "1.2.3-alpha..1", "1.2.3-01", "1.2", "1.2.3+"}
	for _, value := range valid {
		if _, ok := parseSemanticVersion(value); !ok {
			t.Errorf("expected %q to be valid", value)
		}
	}
	for _, value := range invalid {
		if _, ok := parseSemanticVersion(value); ok {
			t.Errorf("expected %q to be invalid", value)
		}
	}
}

func TestStoreRejectsSymlinkEscape(t *testing.T) {
	base := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(base, "escape")); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(base, "http://example.test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("escape/secret"); err == nil {
		t.Fatal("expected symlink escape to be rejected")
	}
	if err := store.Put("escape/new", []byte("bad")); err == nil {
		t.Fatal("expected write through symlink to be rejected")
	}
}

func TestPublicArtifactPathAllowlist(t *testing.T) {
	allowed := []string{
		"providers/acme/example/1.2.3/terraform-provider-example_1.2.3_linux_amd64.zip",
		"modules/acme/vpc/aws/1.2.3/module.tar.gz",
	}
	denied := []string{
		"keys.json",
		"providers/acme/example/index.json",
		"providers/acme/example/1.2.3/metadata.json",
		"providers/acme/example/1.2.3/linux_amd64.json",
		"modules/acme/vpc/aws/1.2.3/metadata.json",
		"tmp/.tmp-secret",
	}
	for _, path := range allowed {
		if !isPublicArtifactPath(path) {
			t.Errorf("expected %q to be allowed", path)
		}
	}
	for _, path := range denied {
		if isPublicArtifactPath(path) {
			t.Errorf("expected %q to be denied", path)
		}
	}
}

func TestKeyStoreRejectsEmptyConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	if err := os.WriteFile(path, []byte(`{"keys":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := NewKeyStore(path, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil {
		t.Fatal("expected empty key configuration to fail closed")
	}
}

func TestKeyStoreRejectsAllDisabledConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	hash := hashKey("disabled-secret")
	data := []byte(`{"keys":[{"hash":"` + hash + `","name":"disabled","permission":"admin","enabled":false}]}`)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	_, err := NewKeyStore(path, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil {
		t.Fatal("expected all-disabled key configuration to fail closed")
	}
}

func TestKeyStoreMigratesLegacyPlaintextKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	data := []byte(`{"keys":[{"key":"legacy-secret","name":"legacy","permission":"admin","enabled":true}]}`)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	ks, err := NewKeyStore(path, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if !ks.HasPermission("legacy-secret", PermAdmin) {
		t.Fatal("migrated key does not authenticate")
	}
	sanitized, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sanitized, []byte("legacy-secret")) {
		t.Fatalf("legacy plaintext remained in key file: %s", sanitized)
	}
}

func TestGarbageCollectionRequiresAdmin(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "http://example.test/api/v1/gc", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := requiredPermission(req); got != PermAdmin {
		t.Fatalf("GC permission = %q, want %q", got, PermAdmin)
	}
}

func TestNetworkMirrorMissingVersionReturnsNotFound(t *testing.T) {
	router, store, dir := setupTestEnv(t)
	defer func() { _ = os.RemoveAll(dir) }()
	if err := store.AddProviderVersion("acme", "example", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/registry.terraform.io/acme/example/9.9.9.json", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", response.Code, response.Body.String())
	}
}

func TestProviderDownloadMetadataRequiresArtifact(t *testing.T) {
	router, store, dir := setupTestEnv(t)
	defer func() { _ = os.RemoveAll(dir) }()
	meta := PlatformMeta{OS: "linux", Arch: "amd64", Filename: "terraform-provider-example_1.0.0_linux_amd64.zip", Shasum: strings.Repeat("a", 64)}
	data, _ := json.Marshal(meta)
	if err := store.Put("providers/acme/example/1.0.0/linux_amd64.json", data); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/providers/acme/example/1.0.0/download/linux/amd64", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.Code)
	}
}

func TestProviderArchiveRejectsTraversal(t *testing.T) {
	var body bytes.Buffer
	zw := zip.NewWriter(&body)
	entry, _ := zw.Create("../terraform-provider-example_v1.0.0")
	_, _ = entry.Write([]byte("binary"))
	_ = zw.Close()
	file, err := os.CreateTemp(t.TempDir(), "provider-*.zip")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	_, _ = file.Write(body.Bytes())
	if err := validateProviderArchive(file, int64(body.Len()), "example", 1<<20); err == nil {
		t.Fatal("expected traversal entry to be rejected")
	}
}

func TestModuleArchiveRejectsSymlink(t *testing.T) {
	var body bytes.Buffer
	gw := gzip.NewWriter(&body)
	tw := tar.NewWriter(gw)
	_ = tw.WriteHeader(&tar.Header{Name: "main.tf", Typeflag: tar.TypeReg, Mode: 0644, Size: 0})
	_ = tw.WriteHeader(&tar.Header{Name: "escape", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"})
	_ = tw.Close()
	_ = gw.Close()
	file, err := os.CreateTemp(t.TempDir(), "module-*.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	_, _ = file.Write(body.Bytes())
	_, _ = file.Seek(0, io.SeekStart)
	if err := validateModuleArchive(file, int64(body.Len()), 1<<20); err == nil {
		t.Fatal("expected symlink entry to be rejected")
	}
}

func TestRegistrySignerPersistsAndProducesVerifiableSignatures(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "keys", "signing-key.asc")
	first, err := NewRegistrySigner(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("signing key mode = %o, want 600", info.Mode().Perm())
	}

	second, err := NewRegistrySigner(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if second.KeyID != first.KeyID || second.PublicArmor != first.PublicArmor {
		t.Fatal("reloaded signer identity changed")
	}

	payload := []byte("artifact checksums\n")
	signature, err := second.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := openpgp.ReadArmoredKeyRing(bytes.NewBufferString(second.PublicArmor))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(signature, []byte("BEGIN PGP SIGNATURE")) {
		t.Fatal("signature must use the binary detached wire format")
	}
	if _, err := openpgp.CheckDetachedSignature(keyring, bytes.NewReader(payload), bytes.NewReader(signature), nil); err != nil {
		t.Fatalf("verify detached signature: %v", err)
	}
}

func TestProviderArchiveRejectsCorruptEntryData(t *testing.T) {
	var body bytes.Buffer
	zw := zip.NewWriter(&body)
	header := &zip.FileHeader{Name: "terraform-provider-example_v1.0.0", Method: zip.Store}
	entry, err := zw.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("known provider payload")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	data := body.Bytes()
	index := bytes.Index(data, []byte("known provider payload"))
	if index < 0 {
		t.Fatal("could not locate stored ZIP payload")
	}
	data[index] ^= 0xff
	file, err := os.CreateTemp(t.TempDir(), "provider-corrupt-*.zip")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := validateProviderArchive(file, int64(len(data)), "example", 1<<20); err == nil {
		t.Fatal("expected corrupt ZIP entry to be rejected")
	}
}

func TestProviderBinaryNameMatching(t *testing.T) {
	prefix := "terraform-provider-example"
	valid := []string{
		prefix,
		prefix + ".exe",
		prefix + "_v1.2.3",
		prefix + "_v1.2.3-alpha-beta.1_x5",
		prefix + "_v1.2.3_x5.exe",
	}
	invalid := []string{
		prefix + "other_v1.2.3",
		prefix + "_vnot-semver",
		prefix + "_v1.2.3_x",
		prefix + "_v1.2.3_xfive",
	}
	for _, name := range valid {
		if !providerBinaryNameMatches(name, prefix) {
			t.Errorf("expected %q to match", name)
		}
	}
	for _, name := range invalid {
		if providerBinaryNameMatches(name, prefix) {
			t.Errorf("expected %q not to match", name)
		}
	}
}

func TestProviderArchiveRejectsDifferentProviderPrefix(t *testing.T) {
	var body bytes.Buffer
	zw := zip.NewWriter(&body)
	entry, err := zw.Create("terraform-provider-awscc_v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("binary")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	file, err := os.CreateTemp(t.TempDir(), "provider-prefix-*.zip")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Write(body.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := validateProviderArchive(file, int64(body.Len()), "aws", 1<<20); err == nil {
		t.Fatal("expected a different provider name sharing the prefix to be rejected")
	}
}

func TestModuleArchiveRejectsInvalidGzipTrailer(t *testing.T) {
	var body bytes.Buffer
	gw := gzip.NewWriter(&body)
	tw := tar.NewWriter(gw)
	content := []byte("terraform {}\n")
	if err := tw.WriteHeader(&tar.Header{Name: "main.tf", Typeflag: tar.TypeReg, Mode: 0644, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	valid := append([]byte(nil), body.Bytes()...)
	corruptCRC := append([]byte(nil), valid...)
	corruptCRC[len(corruptCRC)-8] ^= 0xff
	cases := map[string][]byte{
		"corrupt CRC":       corruptCRC,
		"truncated trailer": append([]byte(nil), valid[:len(valid)-8]...),
		"trailing garbage":  append(append([]byte(nil), valid...), 0xde, 0xad, 0xbe, 0xef),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			file, err := os.CreateTemp(t.TempDir(), "module-corrupt-*.tar.gz")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = file.Close() }()
			if _, err := file.Write(data); err != nil {
				t.Fatal(err)
			}
			if _, err := file.Seek(0, io.SeekStart); err != nil {
				t.Fatal(err)
			}
			if err := validateModuleArchive(file, int64(len(data)), 1<<20); err == nil {
				t.Fatal("expected an invalid gzip stream to be rejected")
			}
		})
	}
}
