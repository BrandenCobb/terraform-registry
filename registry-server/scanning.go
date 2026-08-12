package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ScanMode string
type ScanStatus string
type ArtifactKind string
type Severity string
type PolicyResult string

const (
	ScanModeVisibility ScanMode = "visibility"
	ScanModeQuarantine ScanMode = "quarantine"
	ScanModeEnforce    ScanMode = "enforce"

	ScanQueued   ScanStatus = "queued"
	ScanScanning ScanStatus = "scanning"
	ScanClean    ScanStatus = "clean"
	ScanFindings ScanStatus = "findings"
	ScanError    ScanStatus = "error"
	ScanStale    ScanStatus = "stale"
	ScanDisabled ScanStatus = "disabled"

	ArtifactProvider ArtifactKind = "provider"
	ArtifactModule   ArtifactKind = "module"

	SeverityUnknown  Severity = "UNKNOWN"
	SeverityLow      Severity = "LOW"
	SeverityMedium   Severity = "MEDIUM"
	SeverityHigh     Severity = "HIGH"
	SeverityCritical Severity = "CRITICAL"

	PolicyAllow PolicyResult = "allow"
	PolicyDeny  PolicyResult = "deny"
)

const (
	defaultScanTimeout   = 10 * time.Minute
	defaultScanStale     = 24 * time.Hour
	defaultScanInterval  = time.Hour
	defaultMaxReportSize = int64(10 << 20)
	maxScanWorkers       = 16
	maxScanReportMB      = 100
	maxExtractedFiles    = 10000
)

type ScanConfig struct {
	Enabled       bool
	Mode          ScanMode
	Workers       int
	Timeout       time.Duration
	StaleAfter    time.Duration
	Interval      time.Duration
	MaxReportSize int64
	TrivyPath     string
	CheckovPath   string
	Deny          map[Severity]bool
	Offline       bool
	TrivyCacheDir string
}

func LoadScanConfig() (ScanConfig, error) {
	cfg := ScanConfig{
		Mode: ScanModeVisibility, Workers: 1, Timeout: defaultScanTimeout,
		StaleAfter: defaultScanStale, Interval: defaultScanInterval,
		MaxReportSize: defaultMaxReportSize, TrivyPath: "trivy", CheckovPath: "checkov",
		Deny: map[Severity]bool{SeverityCritical: true, SeverityHigh: true},
	}
	var err error
	if raw := strings.TrimSpace(os.Getenv("SCANNING_ENABLED")); raw != "" {
		cfg.Enabled, err = strconv.ParseBool(raw)
		if err != nil {
			return cfg, fmt.Errorf("SCANNING_ENABLED: %w", err)
		}
	}
	if raw := strings.TrimSpace(os.Getenv("SCAN_MODE")); raw != "" {
		cfg.Mode = ScanMode(strings.ToLower(raw))
	}
	if cfg.Mode != ScanModeVisibility && cfg.Mode != ScanModeQuarantine && cfg.Mode != ScanModeEnforce {
		return cfg, fmt.Errorf("SCAN_MODE must be visibility, quarantine, or enforce")
	}
	if raw := strings.TrimSpace(os.Getenv("SCAN_WORKERS")); raw != "" {
		cfg.Workers, err = strconv.Atoi(raw)
		if err != nil || cfg.Workers < 1 || cfg.Workers > maxScanWorkers {
			return cfg, fmt.Errorf("SCAN_WORKERS must be between 1 and %d", maxScanWorkers)
		}
	}
	if cfg.Timeout, err = envDuration("SCAN_TIMEOUT", cfg.Timeout); err != nil {
		return cfg, err
	}
	if cfg.StaleAfter, err = envDuration("SCAN_STALE_AFTER", cfg.StaleAfter); err != nil {
		return cfg, err
	}
	if cfg.Interval, err = envDuration("SCAN_INTERVAL", cfg.Interval); err != nil {
		return cfg, err
	}
	if cfg.Timeout <= 0 || cfg.StaleAfter <= 0 || cfg.Interval <= 0 {
		return cfg, fmt.Errorf("scan durations must be positive")
	}
	if raw := strings.TrimSpace(os.Getenv("SCAN_MAX_REPORT_MB")); raw != "" {
		mb, parseErr := strconv.Atoi(raw)
		if parseErr != nil || mb < 1 || mb > maxScanReportMB {
			return cfg, fmt.Errorf("SCAN_MAX_REPORT_MB must be between 1 and %d", maxScanReportMB)
		}
		cfg.MaxReportSize = int64(mb) << 20
	}
	if raw := strings.TrimSpace(os.Getenv("TRIVY_PATH")); raw != "" {
		cfg.TrivyPath = raw
	}
	if raw := strings.TrimSpace(os.Getenv("CHECKOV_PATH")); raw != "" {
		cfg.CheckovPath = raw
	}
	if raw := strings.TrimSpace(os.Getenv("SCAN_DENY_SEVERITIES")); raw != "" {
		cfg.Deny = make(map[Severity]bool)
		for _, item := range strings.Split(raw, ",") {
			sev := normalizeSeverity(item)
			if sev == SeverityUnknown {
				return cfg, fmt.Errorf("SCAN_DENY_SEVERITIES contains invalid severity %q", item)
			}
			cfg.Deny[sev] = true
		}
	}
	if raw := strings.TrimSpace(os.Getenv("SCAN_OFFLINE")); raw != "" {
		cfg.Offline, err = strconv.ParseBool(raw)
		if err != nil {
			return cfg, fmt.Errorf("SCAN_OFFLINE: %w", err)
		}
	}
	cfg.TrivyCacheDir = strings.TrimSpace(os.Getenv("TRIVY_CACHE_DIR"))
	return cfg, nil
}

func envDuration(name string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return d, nil
}

type Finding struct {
	ID               string   `json:"id"`
	Severity         Severity `json:"severity"`
	Title            string   `json:"title,omitempty"`
	Description      string   `json:"description,omitempty"`
	Package          string   `json:"package,omitempty"`
	Resource         string   `json:"resource,omitempty"`
	InstalledVersion string   `json:"installed_version,omitempty"`
	FixedVersion     string   `json:"fixed_version,omitempty"`
	File             string   `json:"file,omitempty"`
	StartLine        int      `json:"start_line,omitempty"`
	EndLine          int      `json:"end_line,omitempty"`
	AdvisoryURL      string   `json:"advisory_url,omitempty"`
}

type FindingCounts struct {
	Critical int64 `json:"critical"`
	High     int64 `json:"high"`
	Medium   int64 `json:"medium"`
	Low      int64 `json:"low"`
	Unknown  int64 `json:"unknown"`
}

type ScanSummary struct {
	Status          ScanStatus    `json:"status"`
	PolicyResult    PolicyResult  `json:"policy_result,omitempty"`
	HighestSeverity Severity      `json:"highest_severity,omitempty"`
	Counts          FindingCounts `json:"counts"`
	Scanner         string        `json:"scanner,omitempty"`
	ScannedAt       time.Time     `json:"scanned_at,omitempty"`
	DatabaseUpdated time.Time     `json:"database_updated_at,omitempty"`
	Message         string        `json:"message,omitempty"`
}

type ScanRecord struct {
	ID              string       `json:"id"`
	Digest          string       `json:"digest"`
	Kind            ArtifactKind `json:"kind"`
	ArtifactKey     string       `json:"artifact_key,omitempty"`
	Namespace       string       `json:"namespace,omitempty"`
	Name            string       `json:"name,omitempty"`
	Provider        string       `json:"provider,omitempty"`
	Version         string       `json:"version,omitempty"`
	Platform        string       `json:"platform,omitempty"`
	Scanner         string       `json:"scanner"`
	ScannerVersion  string       `json:"scanner_version,omitempty"`
	DatabaseUpdated time.Time    `json:"database_updated_at,omitempty"`
	PolicyVersion   string       `json:"policy_version,omitempty"`
	PolicyResult    PolicyResult `json:"policy_result,omitempty"`
	Status          ScanStatus   `json:"status"`
	Message         string       `json:"message,omitempty"`
	Findings        []Finding    `json:"findings,omitempty"`
	QueuedAt        time.Time    `json:"queued_at,omitempty"`
	StartedAt       time.Time    `json:"started_at,omitempty"`
	CompletedAt     time.Time    `json:"completed_at,omitempty"`
	DurationMS      int64        `json:"duration_ms,omitempty"`
	PassedChecks    int          `json:"passed_checks,omitempty"`
	FailedChecks    int          `json:"failed_checks,omitempty"`
	SkippedChecks   int          `json:"skipped_checks,omitempty"`
}

func (r *ScanRecord) EffectiveStatus(now time.Time, staleAfter time.Duration) ScanStatus {
	if r == nil {
		return ScanDisabled
	}
	if (r.Status == ScanClean || r.Status == ScanFindings) && !r.CompletedAt.IsZero() && now.Sub(r.CompletedAt) > staleAfter {
		return ScanStale
	}
	return r.Status
}

func (r *ScanRecord) Summary(now time.Time, staleAfter time.Duration) ScanSummary {
	s := ScanSummary{Status: r.EffectiveStatus(now, staleAfter), PolicyResult: r.PolicyResult, Scanner: r.Scanner, ScannedAt: r.CompletedAt, DatabaseUpdated: r.DatabaseUpdated, Message: r.Message}
	for _, f := range r.Findings {
		switch f.Severity {
		case SeverityCritical:
			s.Counts.Critical++
		case SeverityHigh:
			s.Counts.High++
		case SeverityMedium:
			s.Counts.Medium++
		case SeverityLow:
			s.Counts.Low++
		default:
			s.Counts.Unknown++
		}
		if severityRank(f.Severity) > severityRank(s.HighestSeverity) {
			s.HighestSeverity = f.Severity
		}
	}
	return s
}

func normalizeSeverity(s string) Severity {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "CRITICAL":
		return SeverityCritical
	case "HIGH":
		return SeverityHigh
	case "MEDIUM", "MODERATE":
		return SeverityMedium
	case "LOW":
		return SeverityLow
	default:
		return SeverityUnknown
	}
}
func severityRank(s Severity) int {
	switch s {
	case SeverityCritical:
		return 5
	case SeverityHigh:
		return 4
	case SeverityMedium:
		return 3
	case SeverityLow:
		return 2
	case SeverityUnknown:
		return 1
	default:
		return 0
	}
}

func parseTrivyReport(raw []byte) ([]Finding, time.Time, error) {
	var report struct {
		CreatedAt time.Time `json:"CreatedAt"`
		Metadata  struct {
			OS struct {
				EOSL bool `json:"EOSL"`
			} `json:"OS"`
		} `json:"Metadata"`
		Results []struct {
			Target          string `json:"Target"`
			Vulnerabilities []struct {
				ID          string `json:"VulnerabilityID"`
				Package     string `json:"PkgName"`
				Installed   string `json:"InstalledVersion"`
				Fixed       string `json:"FixedVersion"`
				Severity    string `json:"Severity"`
				Title       string `json:"Title"`
				Description string `json:"Description"`
				URL         string `json:"PrimaryURL"`
			} `json:"Vulnerabilities"`
		} `json:"Results"`
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		return nil, time.Time{}, fmt.Errorf("parse trivy report: %w", err)
	}
	var findings []Finding
	for _, result := range report.Results {
		for _, v := range result.Vulnerabilities {
			if strings.TrimSpace(v.ID) == "" {
				continue
			}
			findings = append(findings, Finding{ID: bounded(v.ID, 256), Severity: normalizeSeverity(v.Severity), Title: bounded(v.Title, 2048), Description: bounded(v.Description, 16384), Package: bounded(v.Package, 1024), InstalledVersion: bounded(v.Installed, 1024), FixedVersion: bounded(v.Fixed, 1024), AdvisoryURL: safeAdvisoryURL(v.URL)})
		}
	}
	sortFindings(findings)
	return findings, report.CreatedAt, nil
}

func parseCheckovReport(raw []byte) ([]Finding, error) {
	findings, _, _, _, err := parseCheckovReportDetailed(raw)
	return findings, err
}

func parseCheckovReportDetailed(raw []byte) ([]Finding, int, int, int, error) {
	type check struct {
		ID        string `json:"check_id"`
		Name      string `json:"check_name"`
		File      string `json:"file_path"`
		Lines     []int  `json:"file_line_range"`
		Resource  string `json:"resource"`
		Guideline string `json:"guideline"`
		Severity  string `json:"severity"`
	}
	type report struct {
		Results struct {
			Failed []check `json:"failed_checks"`
		} `json:"results"`
		Summary struct {
			Passed        int `json:"passed"`
			Failed        int `json:"failed"`
			Skipped       int `json:"skipped"`
			ParsingErrors int `json:"parsing_errors"`
		} `json:"summary"`
	}
	var reports []report
	if err := json.Unmarshal(raw, &reports); err != nil {
		var one report
		if oneErr := json.Unmarshal(raw, &one); oneErr != nil {
			return nil, 0, 0, 0, fmt.Errorf("parse checkov report: %w", err)
		}
		reports = []report{one}
	}
	var findings []Finding
	passed, failed, skipped := 0, 0, 0
	for _, rp := range reports {
		if rp.Summary.ParsingErrors > 0 {
			return nil, 0, 0, 0, fmt.Errorf("checkov reported %d parsing errors", rp.Summary.ParsingErrors)
		}
		passed += rp.Summary.Passed
		failed += rp.Summary.Failed
		skipped += rp.Summary.Skipped
		for _, c := range rp.Results.Failed {
			start, end := 0, 0
			if len(c.Lines) > 0 {
				start = c.Lines[0]
			}
			if len(c.Lines) > 1 {
				end = c.Lines[1]
			}
			severity := normalizeSeverity(c.Severity)
			if severity == SeverityUnknown {
				severity = SeverityMedium
			}
			findings = append(findings, Finding{ID: bounded(c.ID, 256), Severity: severity, Title: bounded(c.Name, 2048), Resource: bounded(c.Resource, 2048), File: safeReportPath(c.File), StartLine: start, EndLine: end, AdvisoryURL: safeAdvisoryURL(c.Guideline)})
		}
	}
	sortFindings(findings)
	return findings, passed, failed, skipped, nil
}

func sortFindings(f []Finding) {
	sort.SliceStable(f, func(i, j int) bool {
		ri, rj := severityRank(f[i].Severity), severityRank(f[j].Severity)
		if ri != rj {
			return ri > rj
		}
		return f[i].ID < f[j].ID
	})
}
func bounded(s string, n int) string {
	s = strings.ToValidUTF8(strings.TrimSpace(s), "")
	if len(s) > n {
		return s[:n]
	}
	return s
}
func safeReportPath(s string) string {
	s = strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(s)), "/")
	s = filepath.ToSlash(filepath.Clean(s))
	if s == "." || strings.HasPrefix(s, "../") {
		return ""
	}
	return bounded(s, 4096)
}
func safeAdvisoryURL(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://") {
		return bounded(s, 4096)
	}
	return ""
}

type ScanRepository struct {
	root string
	mu   sync.RWMutex
}

func NewScanRepository(storageRoot string) (*ScanRepository, error) {
	root := filepath.Join(storageRoot, "scans")
	if err := os.MkdirAll(filepath.Join(root, "sha256"), 0750); err != nil {
		return nil, fmt.Errorf("create scan repository: %w", err)
	}
	return &ScanRepository{root: root}, nil
}
func validDigest(d string) bool {
	if len(d) != 64 {
		return false
	}
	_, err := hex.DecodeString(d)
	return err == nil
}
func (r *ScanRepository) digestDir(d string) (string, error) {
	if !validDigest(d) {
		return "", fmt.Errorf("invalid artifact digest")
	}
	return filepath.Join(r.root, "sha256", d[:2], d), nil
}
func (r *ScanRepository) Save(rec *ScanRecord, raw []byte) error {
	if rec == nil || rec.ID == "" || strings.ContainsAny(rec.ID, "/\\") {
		return fmt.Errorf("invalid scan record")
	}
	if int64(len(raw)) > int64(maxScanReportMB)<<20 {
		return fmt.Errorf("raw report too large")
	}
	dir, err := r.digestDir(rec.Digest)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	history := filepath.Join(dir, "history")
	if err = os.MkdirAll(history, 0750); err != nil {
		return err
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	if err = atomicWriteScan(filepath.Join(history, rec.ID+".json"), data); err != nil {
		return err
	}
	if len(raw) > 0 {
		if err = atomicWriteScan(filepath.Join(history, rec.ID+".raw.json"), raw); err != nil {
			return err
		}
	}
	return atomicWriteScan(filepath.Join(dir, "current.json"), data)
}
func atomicWriteScan(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-scan-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}
func (r *ScanRepository) Current(d string) (*ScanRecord, error) {
	dir, err := r.digestDir(d)
	if err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	data, err := os.ReadFile(filepath.Join(dir, "current.json")) // #nosec G304 -- dir is derived from a validated SHA-256 under the private scan root.
	if err != nil {
		return nil, err
	}
	var rec ScanRecord
	if err = json.Unmarshal(data, &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}
func (r *ScanRepository) History(d string, limit int) ([]ScanRecord, error) {
	if limit < 1 || limit > 100 {
		return nil, fmt.Errorf("limit must be 1..100")
	}
	dir, err := r.digestDir(d)
	if err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	entries, err := os.ReadDir(filepath.Join(dir, "history"))
	if err != nil {
		return nil, err
	}
	var out []ScanRecord
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || strings.HasSuffix(e.Name(), ".raw.json") {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(dir, "history", e.Name())) // #nosec G304 -- ReadDir supplies a basename inside validated digest history.
		if readErr != nil {
			continue
		}
		var rec ScanRecord
		if json.Unmarshal(data, &rec) == nil {
			out = append(out, rec)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].QueuedAt.After(out[j].QueuedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (r *ScanRepository) Raw(d, id string) ([]byte, error) {
	if id == "" || strings.ContainsAny(id, "/\\") {
		return nil, fmt.Errorf("invalid scan id")
	}
	dir, err := r.digestDir(d)
	if err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return os.ReadFile(filepath.Join(dir, "history", id+".raw.json")) // #nosec G304 -- digest and scan ID are strictly validated.
}

func (r *ScanRepository) CurrentRecords() ([]ScanRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var records []ScanRecord
	err := filepath.WalkDir(filepath.Join(r.root, "sha256"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || entry.Name() != "current.json" {
			return nil
		}
		data, readErr := os.ReadFile(path) // #nosec G304,G122 -- private scan tree is process-owned and single-writer.
		if readErr != nil {
			return readErr
		}
		var record ScanRecord
		if unmarshalErr := json.Unmarshal(data, &record); unmarshalErr != nil {
			return unmarshalErr
		}
		records = append(records, record)
		return nil
	})
	return records, err
}

type Waiver struct {
	ID        string    `json:"id"`
	Digest    string    `json:"digest"`
	FindingID string    `json:"finding_id,omitempty"`
	Owner     string    `json:"owner"`
	Reason    string    `json:"reason"`
	CreatedBy string    `json:"created_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (w *Waiver) Active(now time.Time) bool {
	return w != nil && w.Owner != "" && w.Reason != "" && !w.ExpiresAt.IsZero() && now.Before(w.ExpiresAt)
}
func policyAllows(mode ScanMode, rec *ScanRecord, waivers []*Waiver, now time.Time, staleAfter time.Duration) bool {
	if mode == ScanModeVisibility {
		return true
	}
	if rec == nil {
		return false
	}
	for _, w := range waivers {
		if w.Digest == rec.Digest && w.Active(now) && (w.FindingID == "" || hasFinding(rec, w.FindingID)) {
			return true
		}
	}
	return rec.EffectiveStatus(now, staleAfter) == ScanClean && rec.PolicyResult != PolicyDeny
}
func hasFinding(rec *ScanRecord, id string) bool {
	for _, f := range rec.Findings {
		if f.ID == id {
			return true
		}
	}
	return false
}

var errExtractLimit = errors.New("scan extraction limit exceeded")

func extractProviderForScan(src, dst string, maxBytes int64, maxFiles int) error {
	z, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer z.Close()
	var total int64
	count := 0
	for _, f := range z.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if f.Mode()&os.ModeSymlink != 0 || !f.Mode().IsRegular() {
			return fmt.Errorf("unsafe zip entry %q", f.Name)
		}
		target, err := safeExtractTarget(dst, f.Name)
		if err != nil {
			return err
		}
		count++
		size, sizeErr := strconv.ParseInt(strconv.FormatUint(f.UncompressedSize64, 10), 10, 64)
		if sizeErr != nil || size > maxBytes || size > maxBytes-total {
			return errExtractLimit
		}
		total += size
		if count > maxFiles {
			return errExtractLimit
		}
		if err = os.MkdirAll(filepath.Dir(target), 0750); err != nil {
			return err
		}
		in, err := f.Open()
		if err != nil {
			return err
		}
		err = writeExtracted(target, in, size, maxBytes)
		_ = in.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
func extractModuleForScan(src, dst string, maxBytes int64, maxFiles int) error {
	f, err := os.Open(src) // #nosec G304 -- source is resolved from a validated immutable storage key under Store.basePath.
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var total int64
	count := 0
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if h.Name == "." || h.Name == "./" {
			continue
		}
		switch h.Typeflag {
		case tar.TypeDir:
			target, e := safeExtractTarget(dst, h.Name)
			if e != nil {
				return e
			}
			if e = os.MkdirAll(target, 0750); e != nil {
				return e
			}
		case tar.TypeReg:
			target, e := safeExtractTarget(dst, h.Name)
			if e != nil {
				return e
			}
			count++
			if h.Size < 0 || count > maxFiles || h.Size > maxBytes-total {
				return errExtractLimit
			}
			total += h.Size
			if e = os.MkdirAll(filepath.Dir(target), 0750); e != nil {
				return e
			}
			if e = writeExtracted(target, tr, h.Size, maxBytes); e != nil {
				return e
			}
		default:
			return fmt.Errorf("unsafe tar entry %q", h.Name)
		}
	}
	n, err := io.Copy(io.Discard, io.LimitReader(gz, maxBytes+1))
	if err != nil {
		return err
	}
	if n > maxBytes {
		return errExtractLimit
	}
	return nil
}
func safeExtractTarget(root, name string) (string, error) {
	name = filepath.Clean(filepath.FromSlash(name))
	if name == "." || filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	target := filepath.Join(root, name)
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("archive path escapes workspace")
	}
	return target, nil
}
func writeExtracted(path string, src io.Reader, size, max int64) error {
	if size > max {
		return errExtractLimit
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600) // #nosec G304 -- path is confined by safeExtractTarget to a fresh private workspace.
	if err != nil {
		return err
	}
	_, copyErr := io.CopyN(f, src, size)
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

type CommandRunner struct {
	MaxOutput   int64
	Environment []string
}
type cappedOutput struct {
	mu       sync.Mutex
	buf      bytes.Buffer
	max      int64
	exceeded bool
}

func (w *cappedOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if int64(w.buf.Len()+len(p)) > w.max {
		remaining := int(w.max) - w.buf.Len()
		if remaining > 0 {
			_, _ = w.buf.Write(p[:remaining])
		}
		w.exceeded = true
		return len(p), errors.New("scanner output exceeds configured limit")
	}
	return w.buf.Write(p)
}
func (r CommandRunner) Run(ctx context.Context, path string, args ...string) ([]byte, error) {
	if r.MaxOutput <= 0 {
		return nil, fmt.Errorf("invalid output limit")
	}
	cmd := exec.CommandContext(ctx, path, args...) // #nosec G204 -- executable is operator configuration; arguments are fixed and passed without a shell.
	if r.Environment != nil {
		cmd.Env = append([]string(nil), r.Environment...)
	}
	out := &cappedOutput{max: r.MaxOutput}
	cmd.Stdout = out
	cmd.Stderr = out
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("scanner timed out: %w", ctx.Err())
	}
	if out.exceeded {
		return nil, fmt.Errorf("scanner output exceeds configured limit")
	}
	if err != nil {
		return nil, fmt.Errorf("scanner failed: %w: %s", err, bounded(out.buf.String(), 4096))
	}
	return append([]byte(nil), out.buf.Bytes()...), nil
}

func artifactDigest(path string) (string, error) {
	f, err := os.Open(path) // #nosec G304 -- caller supplies a Store-confined immutable artifact path.
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
