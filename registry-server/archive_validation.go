package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

var ErrInvalidArchive = errors.New("invalid archive")

func archivePathSafe(name string) bool {
	if name == "" || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") {
		return false
	}
	clean := path.Clean(name)
	if clean == "." {
		return name == "." || name == "./"
	}
	return clean != ".." && !strings.HasPrefix(clean, "../")
}

func expandedArchiveLimit(uploadLimit int64) int64 {
	const maxExpanded = int64(20 << 30)
	if uploadLimit <= 0 || uploadLimit > maxExpanded/20 {
		return maxExpanded
	}
	return uploadLimit * 20
}

func validateProviderArchive(f *os.File, size int64, providerName string, uploadLimit int64) error {
	zr, err := zip.NewReader(f, size)
	if err != nil {
		return fmt.Errorf("invalid provider ZIP: %w", err)
	}
	binaryPrefix := "terraform-provider-" + providerName
	foundBinary := false
	var expanded int64
	limit := expandedArchiveLimit(uploadLimit)
	for _, entry := range zr.File {
		if !archivePathSafe(entry.Name) {
			return fmt.Errorf("invalid provider ZIP: unsafe path %q", entry.Name)
		}
		mode := entry.Mode()
		if mode&os.ModeSymlink != 0 || (!mode.IsRegular() && !mode.IsDir()) {
			return fmt.Errorf("invalid provider ZIP: unsupported entry %q", entry.Name)
		}
		if entry.FileInfo().IsDir() {
			continue
		}
		if entry.UncompressedSize64 > uint64(limit-expanded) { // #nosec G115 -- limit-expanded is always non-negative and at most 20 GiB.
			return fmt.Errorf("invalid provider ZIP: expanded size exceeds %d bytes", limit)
		}
		expanded += int64(entry.UncompressedSize64) // #nosec G115 -- the preceding bound proves the value fits in int64.
		reader, err := entry.Open()
		if err != nil {
			return fmt.Errorf("invalid provider ZIP entry %q: %w", entry.Name, err)
		}
		written, copyErr := io.Copy(io.Discard, reader) // #nosec G110 -- total declared expansion is bounded above.
		closeErr := reader.Close()
		if copyErr != nil {
			return fmt.Errorf("invalid provider ZIP entry %q: %w", entry.Name, copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("invalid provider ZIP entry %q: %w", entry.Name, closeErr)
		}
		if written != int64(entry.UncompressedSize64) { // #nosec G115 -- the declared size was bounded above.
			return fmt.Errorf("invalid provider ZIP entry %q: unexpected expanded size", entry.Name)
		}
		if providerBinaryNameMatches(path.Base(entry.Name), binaryPrefix) {
			foundBinary = true
		}
	}
	if !foundBinary {
		return fmt.Errorf("invalid provider ZIP: missing %s binary", binaryPrefix)
	}
	return nil
}

func providerBinaryNameMatches(base, prefix string) bool {
	base = strings.TrimSuffix(base, ".exe")
	if base == prefix {
		return true
	}
	version, found := strings.CutPrefix(base, prefix+"_v")
	if !found {
		return false
	}
	if versionOnly, protocol, hasProtocol := strings.Cut(version, "_x"); hasProtocol {
		if protocol == "" {
			return false
		}
		for _, char := range protocol {
			if char < '0' || char > '9' {
				return false
			}
		}
		version = versionOnly
	}
	_, valid := parseSemanticVersion(version)
	return valid
}

func validateModuleArchive(f *os.File, _ int64, uploadLimit int64) error {
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("invalid module tar.gz: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	remaining := expandedArchiveLimit(uploadLimit)
	foundConfig := false
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("invalid module tar.gz: %w", err)
		}
		if !archivePathSafe(header.Name) {
			return fmt.Errorf("invalid module tar.gz: unsafe path %q", header.Name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg:
		default:
			return fmt.Errorf("invalid module tar.gz: links and special entries are not allowed: %q", header.Name)
		}
		if header.Size < 0 || header.Size > remaining {
			return fmt.Errorf("invalid module tar.gz: expanded size exceeds %d bytes", expandedArchiveLimit(uploadLimit))
		}
		written, err := io.Copy(io.Discard, tr) // #nosec G110 -- entry and cumulative expansion are bounded above.
		if err != nil || written != header.Size {
			if err == nil {
				err = io.ErrUnexpectedEOF
			}
			return fmt.Errorf("invalid module tar.gz entry %q: %w", header.Name, err)
		}
		remaining -= written
		base := path.Base(header.Name)
		if strings.HasSuffix(base, ".tf") || strings.HasSuffix(base, ".tf.json") {
			foundConfig = true
		}
	}
	if _, err := io.Copy(io.Discard, gz); err != nil { // #nosec G110 -- compressed and expanded input sizes are bounded.
		return fmt.Errorf("invalid module tar.gz trailer: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("invalid module tar.gz: %w", err)
	}
	if !foundConfig {
		return fmt.Errorf("invalid module tar.gz: no Terraform configuration files")
	}
	return nil
}
