package main

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestScannerVersionNormalization(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	dir := t.TempDir()
	trivy := filepath.Join(dir, "trivy")
	checkov := filepath.Join(dir, "checkov")
	if err := os.WriteFile(trivy, []byte("#!/bin/sh\nprintf 'Version: 0.73.0\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(checkov, []byte("#!/bin/sh\nprintf '3.3.9\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := &ScanManager{cfg: ScanConfig{TrivyPath: trivy, CheckovPath: checkov}, ctx: ctx}
	if got := m.scannerVersion(ArtifactProvider); got != "0.73.0" {
		t.Fatalf("provider scanner version = %q", got)
	}
	if got := m.scannerVersion(ArtifactModule); got != "3.3.9" {
		t.Fatalf("module scanner version = %q", got)
	}
}

func TestScanManagerProviderLifecycleAndDeduplication(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	root := t.TempDir()
	s, err := NewStore(root, "http://localhost", testLogger())
	if err != nil {
		t.Fatal(err)
	}
	artifactKey := "providers/acme/demo/1.0.0/terraform-provider-demo_1.0.0_linux_amd64_" + string(bytes.Repeat([]byte{'a'}, 64)) + ".zip"
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	w, _ := zw.Create("terraform-provider-demo_v1.0.0")
	_, _ = w.Write([]byte("binary"))
	_ = zw.Close()
	if err := s.Put(artifactKey, archive.Bytes()); err != nil {
		t.Fatal(err)
	}
	digest, err := artifactDigest(filepath.Join(root, artifactKey))
	if err != nil {
		t.Fatal(err)
	}

	script := filepath.Join(root, "fake-trivy")
	output := `{"SchemaVersion":2,"CreatedAt":"2026-08-11T00:00:00Z","Results":[{"Target":"provider","Vulnerabilities":[{"VulnerabilityID":"CVE-2026-42","PkgName":"x","Severity":"HIGH"}]}]}`
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s' '"+output+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	cfg := ScanConfig{Enabled: true, Mode: ScanModeVisibility, Workers: 1, Timeout: time.Second, StaleAfter: time.Hour, Interval: time.Hour, MaxReportSize: 1 << 20, TrivyPath: script, CheckovPath: script, Deny: map[Severity]bool{SeverityHigh: true}}
	repo, _ := NewScanRepository(root)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := NewScanManager(cfg, s, repo, NewMetrics(), NewWebhookManager("", testLogger()), testLogger())
	m.Start(ctx)
	defer m.Stop()
	job := ScanJob{Digest: digest, Kind: ArtifactProvider, ArtifactKey: artifactKey, Namespace: "acme", Name: "demo", Version: "1.0.0", Platform: "linux/amd64"}
	if queued, err := m.Enqueue(job, false); err != nil || !queued {
		t.Fatalf("enqueue: %v %v", queued, err)
	}
	if queued, err := m.Enqueue(job, false); err != nil || queued {
		t.Fatalf("dedupe failed: %v %v", queued, err)
	}
	waitForScanStatus(t, repo, digest, ScanFindings)
	rec, _ := repo.Current(digest)
	if rec.PolicyResult != PolicyDeny || len(rec.Findings) != 1 {
		t.Fatalf("unexpected record: %+v", rec)
	}
	if queued, err := m.Enqueue(job, true); err != nil || !queued {
		t.Fatalf("forced rescan: %v %v", queued, err)
	}
	waitForHistory(t, repo, digest, 2)
}

func TestScanManagerRecoversQueuedAndInterruptedRecords(t *testing.T) {
	root := t.TempDir()
	s, _ := NewStore(root, "http://localhost", testLogger())
	repo, _ := NewScanRepository(root)
	for i, status := range []ScanStatus{ScanQueued, ScanScanning} {
		d := string(bytes.Repeat([]byte{byte('c' + i)}, 64))
		r := &ScanRecord{ID: newScanID(), Digest: d, Kind: ArtifactProvider, ArtifactKey: "missing", Scanner: "trivy", Status: status, QueuedAt: time.Now().UTC()}
		if err := repo.Save(r, nil); err != nil {
			t.Fatal(err)
		}
	}
	cfg := ScanConfig{Enabled: true, Mode: ScanModeVisibility, Workers: 1, Timeout: time.Second, StaleAfter: time.Hour, Interval: time.Hour, MaxReportSize: 1 << 20, TrivyPath: "missing", CheckovPath: "missing", Deny: map[Severity]bool{}}
	m := NewScanManager(cfg, s, repo, NewMetrics(), NewWebhookManager("", testLogger()), testLogger())
	jobs, err := m.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if jobs != 2 {
		t.Fatalf("expected 2 recovered jobs, got %d", jobs)
	}
}

func waitForScanStatus(t *testing.T, repo *ScanRepository, digest string, want ScanStatus) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if r, e := repo.Current(digest); e == nil && r.Status == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	r, _ := repo.Current(digest)
	t.Fatalf("timed out waiting for %s, got %+v", want, r)
}
func waitForHistory(t *testing.T, repo *ScanRepository, digest string, count int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if h, e := repo.History(digest, 100); e == nil && len(h) >= count {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	h, _ := repo.History(digest, 100)
	t.Fatalf("timed out waiting for history %d, got %d", count, len(h))
}
