package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func createZip(sourceFile, destPath string) (err error) {
	if err = os.MkdirAll(filepath.Dir(destPath), 0750); err != nil {
		return err
	}
	f, err := os.Create(destPath) // #nosec G304 -- destination is explicitly selected by the CLI user.
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(destPath)
		}
	}()
	w := zip.NewWriter(f)
	src, err := os.Open(sourceFile) // #nosec G304 -- source is explicitly selected by the CLI user.
	if err != nil {
		return err
	}
	info, err := src.Stat()
	if err != nil {
		_ = src.Close()
		return err
	}
	header, err := zip.FileInfoHeader(info)
	if err != nil {
		_ = src.Close()
		return err
	}
	header.Method = zip.Deflate
	header.Name = filepath.Base(sourceFile)
	writer, err := w.CreateHeader(header)
	if err == nil {
		_, err = io.Copy(writer, src)
	}
	if closeErr := src.Close(); err == nil {
		err = closeErr
	}
	if closeErr := w.Close(); err == nil {
		err = closeErr
	}
	return err
}

func createTarGz(sourceDir, destPath string) (err error) {
	if err = os.MkdirAll(filepath.Dir(destPath), 0750); err != nil {
		return err
	}
	destAbs, err := filepath.Abs(destPath)
	if err != nil {
		return err
	}
	f, err := os.Create(destPath) // #nosec G304 -- destination is explicitly selected by the CLI user.
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(destPath)
		}
	}()
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	err = filepath.Walk(sourceDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		pathAbs, absErr := filepath.Abs(path)
		if absErr != nil {
			return absErr
		}
		if pathAbs == destAbs {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symbolic links are not supported in module bundles: %s", path)
		}
		relPath, relErr := filepath.Rel(sourceDir, path)
		if relErr != nil {
			return relErr
		}
		if relPath == "." {
			return nil
		}
		header, headerErr := tar.FileInfoHeader(info, "")
		if headerErr != nil {
			return headerErr
		}
		header.Name = filepath.ToSlash(relPath)
		if headerErr = tw.WriteHeader(header); headerErr != nil {
			return headerErr
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		file, openErr := os.Open(path) // #nosec G122,G304 -- Walk rejects symlinks; user-selected source is not a security boundary.
		if openErr != nil {
			return openErr
		}
		_, copyErr := io.Copy(tw, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if closeErr := tw.Close(); err == nil {
		err = closeErr
	}
	if closeErr := gw.Close(); err == nil {
		err = closeErr
	}
	return err
}
