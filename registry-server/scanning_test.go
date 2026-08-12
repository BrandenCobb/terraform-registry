package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestLoadScanConfigDefaultsDisabled(t *testing.T) {
	t.Setenv("SCANNING_ENABLED", "")
	cfg, err := LoadScanConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Enabled || cfg.Mode != ScanModeVisibility || cfg.Workers != 1 {
		t.Fatalf("unsafe defaults: %+v", cfg)
	}
}

func TestLoadScanConfigRejectsInvalidModeAndBounds(t *testing.T) {
	t.Setenv("SCANNING_ENABLED", "true")
	t.Setenv("SCAN_MODE", "destroy")
	if _, err := LoadScanConfig(); err == nil {
		t.Fatal("expected invalid mode rejection")
	}
	t.Setenv("SCAN_MODE", "visibility")
	t.Setenv("SCAN_WORKERS", "0")
	if _, err := LoadScanConfig(); err == nil {
		t.Fatal("expected invalid workers rejection")
	}
}

func TestParseTrivyReportNormalizesFindings(t *testing.T) {
	raw := []byte(`{"SchemaVersion":2,"CreatedAt":"2026-08-11T00:00:00Z","Results":[{"Target":"terraform-provider-x","Vulnerabilities":[{"VulnerabilityID":"CVE-2026-1","PkgName":"crypto/x","InstalledVersion":"1.0","FixedVersion":"1.1","Severity":"CRITICAL","Title":"bad crypto","Description":"details","PrimaryURL":"https://example.test/cve"}]}]}`)
	findings, db, err := parseTrivyReport(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].ID != "CVE-2026-1" || findings[0].Severity != SeverityCritical || findings[0].FixedVersion != "1.1" {
		t.Fatalf("unexpected findings: %+v", findings)
	}
	if db.IsZero() {
		t.Fatal("expected database/report timestamp")
	}
}

func TestParseCheckovReportNormalizesFailedChecks(t *testing.T) {
	raw := []byte(`{"check_type":"terraform","results":{"passed_checks":[],"failed_checks":[{"check_id":"CKV_AWS_1","check_name":"Encrypt it","file_path":"/main.tf","file_line_range":[2,5],"resource":"aws_s3_bucket.x","guideline":"https://example.test/fix","severity":"HIGH"}],"skipped_checks":[]},"summary":{"passed":0,"failed":1,"skipped":0}}`)
	findings, err := parseCheckovReport(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].ID != "CKV_AWS_1" || findings[0].File != "main.tf" || findings[0].StartLine != 2 || findings[0].Resource != "aws_s3_bucket.x" {
		t.Fatalf("unexpected: %+v", findings)
	}
}

func TestScanRecordPersistenceHistoryAndStale(t *testing.T) {
	dir := t.TempDir()
	repo, err := NewScanRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	rec := &ScanRecord{ID: "one", Digest: string(bytes.Repeat([]byte{'a'}, 64)), Kind: ArtifactProvider, Scanner: "trivy", Status: ScanClean, CompletedAt: time.Now().UTC().Add(-48 * time.Hour)}
	if err := repo.Save(rec, []byte(`{"Results":[]}`)); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Current(rec.Digest)
	if err != nil || got.ID != "one" {
		t.Fatalf("current: %+v %v", got, err)
	}
	if got.EffectiveStatus(time.Now(), 24*time.Hour) != ScanStale {
		t.Fatal("expected stale status")
	}
	h, err := repo.History(rec.Digest, 10)
	if err != nil || len(h) != 1 {
		t.Fatalf("history: %+v %v", h, err)
	}
	raw, err := repo.Raw(rec.Digest, "one")
	if err != nil || string(raw) != `{"Results":[]}` {
		t.Fatalf("raw: %s %v", raw, err)
	}
}

func TestPolicyModesAndExpiringWaiver(t *testing.T) {
	now := time.Now().UTC()
	rec := &ScanRecord{Digest: string(bytes.Repeat([]byte{'b'}, 64)), Status: ScanFindings, PolicyResult: PolicyDeny, CompletedAt: now}
	if !policyAllows(ScanModeVisibility, rec, nil, now, 24*time.Hour) {
		t.Fatal("visibility must not block")
	}
	if policyAllows(ScanModeQuarantine, rec, nil, now, 24*time.Hour) {
		t.Fatal("quarantine must block denied result")
	}
	waiver := &Waiver{Digest: rec.Digest, Reason: "risk accepted", Owner: "security", ExpiresAt: now.Add(time.Hour)}
	if !policyAllows(ScanModeEnforce, rec, []*Waiver{waiver}, now, 24*time.Hour) {
		t.Fatal("active waiver must allow")
	}
	waiver.ExpiresAt = now.Add(-time.Second)
	if policyAllows(ScanModeEnforce, rec, []*Waiver{waiver}, now, 24*time.Hour) {
		t.Fatal("expired waiver must not allow")
	}
}

func TestSafeExtractionRejectsZipSymlinkAndTarTraversal(t *testing.T) {
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	h := &zip.FileHeader{Name: "escape", Method: zip.Store}
	h.SetMode(os.ModeSymlink | 0777)
	w, _ := zw.CreateHeader(h)
	_, _ = w.Write([]byte("/etc/passwd"))
	_ = zw.Close()
	zp := filepath.Join(t.TempDir(), "bad.zip")
	_ = os.WriteFile(zp, zb.Bytes(), 0600)
	if err := extractProviderForScan(zp, t.TempDir(), 1<<20, 100); err == nil {
		t.Fatal("expected symlink rejection")
	}

	var tb bytes.Buffer
	gz := gzip.NewWriter(&tb)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "../escape.tf", Mode: 0600, Size: 1, Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte("x"))
	_ = tw.Close()
	_ = gz.Close()
	tp := filepath.Join(t.TempDir(), "bad.tar.gz")
	_ = os.WriteFile(tp, tb.Bytes(), 0600)
	if err := extractModuleForScan(tp, t.TempDir(), 1<<20, 100); err == nil {
		t.Fatal("expected traversal rejection")
	}
}

func TestCommandScannerTimeoutAndOutputLimit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "scanner")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nif [ \"$1\" = slow ]; then sleep 2; else yes x | head -c 10000; fi\n"), 0700); err != nil {
		t.Fatal(err)
	}
	r := CommandRunner{MaxOutput: 128, Environment: []string{"PATH=" + os.Getenv("PATH")}}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := r.Run(ctx, script, "slow"); err == nil {
		t.Fatal("expected timeout")
	}
	if _, err := r.Run(context.Background(), script, "large"); err == nil {
		t.Fatal("expected output limit")
	}
}

func TestScanSummaryUsesWorstSeverityAndCounts(t *testing.T) {
	rec := &ScanRecord{Status: ScanFindings, Findings: []Finding{{Severity: SeverityMedium}, {Severity: SeverityCritical}, {Severity: SeverityHigh}}}
	s := rec.Summary(time.Now(), 24*time.Hour)
	if s.Status != ScanFindings || s.HighestSeverity != SeverityCritical || s.Counts.Critical != 1 || s.Counts.High != 1 {
		b, _ := json.Marshal(s)
		t.Fatal(string(b))
	}
}
