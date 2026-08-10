package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

type RegistrySigner struct {
	entity      *openpgp.Entity
	KeyID       string
	PublicArmor string
}

func NewRegistrySigner(privateKeyPath string) (*RegistrySigner, error) {
	var entity *openpgp.Entity
	data, err := os.ReadFile(privateKeyPath) // #nosec G304 -- operator-configured local key path is intentional.
	if err == nil {
		block, err := armor.Decode(bytes.NewReader(data))
		if err != nil || block.Type != openpgp.PrivateKeyType {
			return nil, fmt.Errorf("decode signing key: %w", err)
		}
		entities, err := openpgp.ReadKeyRing(block.Body)
		if err != nil || len(entities) != 1 {
			return nil, fmt.Errorf("read signing key: %w", err)
		}
		entity = entities[0]
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read signing key: %w", err)
	} else {
		entity, err = openpgp.NewEntity("Terraform Registry", "Artifact Signing", "registry@localhost", &packet.Config{RSABits: 3072})
		if err != nil {
			return nil, fmt.Errorf("generate signing key: %w", err)
		}
		var private bytes.Buffer
		armored, err := armor.Encode(&private, openpgp.PrivateKeyType, nil)
		if err != nil {
			return nil, err
		}
		if err := entity.SerializePrivate(armored, nil); err != nil {
			return nil, err
		}
		if err := armored.Close(); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(privateKeyPath), 0700); err != nil {
			return nil, err
		}
		tmp, err := os.CreateTemp(filepath.Dir(privateKeyPath), ".signing-key-*")
		if err != nil {
			return nil, err
		}
		tmpName := tmp.Name()
		defer func() { _ = os.Remove(tmpName) }()
		if err := tmp.Chmod(0600); err != nil {
			_ = tmp.Close()
			return nil, err
		}
		if _, err := tmp.Write(private.Bytes()); err != nil {
			_ = tmp.Close()
			return nil, err
		}
		if err := tmp.Sync(); err != nil {
			_ = tmp.Close()
			return nil, err
		}
		if err := tmp.Close(); err != nil {
			return nil, err
		}
		if err := os.Rename(tmpName, privateKeyPath); err != nil {
			return nil, err
		}
	}

	var public bytes.Buffer
	armored, err := armor.Encode(&public, openpgp.PublicKeyType, nil)
	if err != nil {
		return nil, err
	}
	if err := entity.Serialize(armored); err != nil {
		return nil, err
	}
	if err := armored.Close(); err != nil {
		return nil, err
	}
	return &RegistrySigner{
		entity:      entity,
		KeyID:       strings.ToUpper(entity.PrimaryKey.KeyIdString()),
		PublicArmor: public.String(),
	}, nil
}

func (s *RegistrySigner) Sign(data []byte) ([]byte, error) {
	var signature bytes.Buffer
	if err := openpgp.DetachSign(&signature, s.entity, bytes.NewReader(data), nil); err != nil {
		return nil, err
	}
	return signature.Bytes(), nil
}

func rebuildAllProviderChecksums() error {
	_, namespacePrefixes, err := store.List("providers/", "/")
	if err != nil {
		return err
	}
	var failures []string
	for _, namespacePrefix := range namespacePrefixes {
		namespace := strings.TrimSuffix(strings.TrimPrefix(namespacePrefix, "providers/"), "/")
		_, providerPrefixes, err := store.List(namespacePrefix, "/")
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		for _, providerPrefix := range providerPrefixes {
			name := strings.TrimSuffix(strings.TrimPrefix(providerPrefix, namespacePrefix), "/")
			versions, err := store.ScanProviderVersions(namespace, name)
			if err != nil {
				failures = append(failures, err.Error())
				continue
			}
			for _, version := range versions {
				if err := rebuildProviderChecksums(namespace, name, version); err != nil {
					failures = append(failures, fmt.Sprintf("%s/%s@%s: %v", namespace, name, version, err))
				}
			}
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("checksum rebuild failures: %s", strings.Join(failures, "; "))
	}
	return nil
}

func providerChecksumsNames(name, version string) (string, string) {
	base := fmt.Sprintf("terraform-provider-%s_%s_SHA256SUMS", name, version)
	return base, base + ".sig"
}

func rebuildProviderChecksums(namespace, name, version string) error {
	if signer == nil {
		return fmt.Errorf("registry signer is unavailable")
	}
	platforms, err := store.GetProviderPlatforms(namespace, name, version)
	if err != nil {
		return err
	}
	type checksumEntry struct{ filename, checksum string }
	entries := make([]checksumEntry, 0, len(platforms))
	for _, platform := range platforms {
		key := fmt.Sprintf("providers/%s/%s/%s/%s", namespace, name, version, platform.Filename)
		if !store.Exists(key) {
			continue
		}
		entries = append(entries, checksumEntry{platform.Filename, platform.Shasum})
	}
	if len(entries) == 0 {
		return fmt.Errorf("provider version has no published artifacts")
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].filename < entries[j].filename })
	var checksums bytes.Buffer
	for _, entry := range entries {
		_, _ = fmt.Fprintf(&checksums, "%s  %s\n", entry.checksum, entry.filename)
	}
	signature, err := signer.Sign(checksums.Bytes())
	if err != nil {
		return err
	}
	checksumsName, signatureName := providerChecksumsNames(name, version)
	prefix := fmt.Sprintf("providers/%s/%s/%s/", namespace, name, version)
	if err := store.Put(prefix+checksumsName, checksums.Bytes()); err != nil {
		return err
	}
	if err := store.Put(prefix+signatureName, signature); err != nil {
		return err
	}
	return nil
}
