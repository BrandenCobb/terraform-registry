package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type ScanJob struct {
	Digest      string       `json:"digest"`
	Kind        ArtifactKind `json:"kind"`
	ArtifactKey string       `json:"artifact_key"`
	Namespace   string       `json:"namespace,omitempty"`
	Name        string       `json:"name,omitempty"`
	Provider    string       `json:"provider,omitempty"`
	Version     string       `json:"version,omitempty"`
	Platform    string       `json:"platform,omitempty"`
}

type ScanManager struct {
	cfg      ScanConfig
	store    *Store
	repo     *ScanRepository
	metrics  *RegistryMetrics
	webhooks *WebhookManager
	logger   *slog.Logger
	queue    chan ScanJob
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	mu       sync.Mutex
	pending  map[string]struct{}
	started  bool
	running  atomic.Int64
}

func NewScanManager(cfg ScanConfig, store *Store, repo *ScanRepository, metrics *RegistryMetrics, webhooks *WebhookManager, logger *slog.Logger) *ScanManager {
	capacity := cfg.Workers * 64
	if capacity < 64 {
		capacity = 64
	}
	return &ScanManager{cfg: cfg, store: store, repo: repo, metrics: metrics, webhooks: webhooks, logger: logger, queue: make(chan ScanJob, capacity), pending: make(map[string]struct{})}
}

func (m *ScanManager) Start(parent context.Context) {
	m.mu.Lock()
	if m.started || !m.cfg.Enabled {
		m.mu.Unlock()
		return
	}
	m.ctx, m.cancel = context.WithCancel(parent)
	m.started = true
	m.mu.Unlock()
	for i := 0; i < m.cfg.Workers; i++ {
		m.wg.Add(1)
		go m.worker()
	}
	m.wg.Add(1)
	go m.scheduler()
	if _, err := m.Recover(); err != nil {
		m.logger.Error("scan queue recovery failed", "error", err)
	}
	if err := m.Backfill(); err != nil {
		m.logger.Error("scan inventory backfill failed", "error", err)
	}
}

func (m *ScanManager) Stop() {
	m.mu.Lock()
	if !m.started {
		m.mu.Unlock()
		return
	}
	m.cancel()
	m.started = false
	m.mu.Unlock()
	m.wg.Wait()
}

func (m *ScanManager) Enqueue(job ScanJob, force bool) (bool, error) {
	if !m.cfg.Enabled {
		return false, nil
	}
	if !validDigest(job.Digest) || (job.Kind != ArtifactProvider && job.Kind != ArtifactModule) || job.ArtifactKey == "" {
		return false, fmt.Errorf("invalid scan job")
	}
	m.mu.Lock()
	if _, exists := m.pending[job.Digest]; exists {
		m.mu.Unlock()
		return false, nil
	}
	if !force {
		if current, err := m.repo.Current(job.Digest); err == nil {
			status := current.EffectiveStatus(time.Now().UTC(), m.cfg.StaleAfter)
			if status != ScanStale && status != ScanError {
				m.mu.Unlock()
				return false, nil
			}
		}
	}
	m.pending[job.Digest] = struct{}{}
	m.mu.Unlock()

	rec := recordFromJob(job)
	if err := m.repo.Save(rec, nil); err != nil {
		m.clearPending(job.Digest)
		return false, err
	}
	select {
	case m.queue <- job:
		if m.metrics != nil {
			m.metrics.ScanQueueDepth.Add(1)
			m.metrics.ScanQueuedTotal.Add(1)
		}
		return true, nil
	default:
		m.clearPending(job.Digest)
		rec.Status = ScanError
		rec.Message = "scan queue is full"
		rec.CompletedAt = time.Now().UTC()
		_ = m.repo.Save(rec, nil)
		return false, fmt.Errorf("scan queue is full")
	}
}

func recordFromJob(job ScanJob) *ScanRecord {
	scanner := "checkov"
	if job.Kind == ArtifactProvider {
		scanner = "trivy"
	}
	return &ScanRecord{ID: newScanID(), Digest: job.Digest, Kind: job.Kind, ArtifactKey: job.ArtifactKey, Namespace: job.Namespace, Name: job.Name, Provider: job.Provider, Version: job.Version, Platform: job.Platform, Scanner: scanner, Status: ScanQueued, QueuedAt: time.Now().UTC()}
}

func (m *ScanManager) Recover() (int, error) {
	records, err := m.repo.CurrentRecords()
	if err != nil {
		return 0, err
	}
	recovered := 0
	for _, rec := range records {
		if rec.Status != ScanQueued && rec.Status != ScanScanning {
			continue
		}
		job := ScanJob{Digest: rec.Digest, Kind: rec.Kind, ArtifactKey: rec.ArtifactKey, Namespace: rec.Namespace, Name: rec.Name, Provider: rec.Provider, Version: rec.Version, Platform: rec.Platform}
		m.mu.Lock()
		if _, exists := m.pending[job.Digest]; exists {
			m.mu.Unlock()
			continue
		}
		m.pending[job.Digest] = struct{}{}
		m.mu.Unlock()
		if rec.Status == ScanScanning {
			rec.Status = ScanQueued
			rec.StartedAt = time.Time{}
			rec.Message = "recovered after interrupted scan"
			if err := m.repo.Save(&rec, nil); err != nil {
				m.clearPending(job.Digest)
				continue
			}
		}
		select {
		case m.queue <- job:
			recovered++
			if m.metrics != nil {
				m.metrics.ScanQueueDepth.Add(1)
			}
		default:
			m.clearPending(job.Digest)
			return recovered, fmt.Errorf("scan queue full during recovery")
		}
	}
	return recovered, nil
}

func (m *ScanManager) Backfill() error {
	if !m.cfg.Enabled {
		return nil
	}
	if err := m.backfillProviders(); err != nil {
		return err
	}
	return m.backfillModules()
}

func (m *ScanManager) backfillProviders() error {
	return filepath.WalkDir(filepath.Join(m.store.basePath, "providers"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || entry.Name() == "index.json" || entry.Name() == "metadata.json" {
			return nil
		}
		data, readErr := os.ReadFile(path) // #nosec G304,G122 -- metadata tree is process-owned under the single-writer Store.
		if readErr != nil {
			return nil
		}
		var meta PlatformMeta
		if json.Unmarshal(data, &meta) != nil || !validDigest(meta.Shasum) || meta.Filename == "" {
			return nil
		}
		rel, relErr := filepath.Rel(m.store.basePath, filepath.Join(filepath.Dir(path), meta.Filename))
		if relErr != nil {
			return nil
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) < 5 {
			return nil
		}
		_, _ = m.Enqueue(ScanJob{Digest: meta.Shasum, Kind: ArtifactProvider, ArtifactKey: filepath.ToSlash(rel), Namespace: parts[1], Name: parts[2], Version: parts[3], Platform: meta.OS + "/" + meta.Arch}, false)
		return nil
	})
}

func (m *ScanManager) backfillModules() error {
	return filepath.WalkDir(filepath.Join(m.store.basePath, "modules"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if entry.IsDir() || entry.Name() != "artifact.json" {
			return nil
		}
		data, readErr := os.ReadFile(path) // #nosec G304,G122 -- metadata tree is process-owned under the single-writer Store.
		if readErr != nil {
			return nil
		}
		var meta ModuleArtifactMeta
		if json.Unmarshal(data, &meta) != nil || !validDigest(meta.SHA256) || meta.Filename == "" {
			return nil
		}
		rel, relErr := filepath.Rel(m.store.basePath, filepath.Join(filepath.Dir(path), meta.Filename))
		if relErr != nil {
			return nil
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) < 6 {
			return nil
		}
		_, _ = m.Enqueue(ScanJob{Digest: meta.SHA256, Kind: ArtifactModule, ArtifactKey: filepath.ToSlash(rel), Namespace: parts[1], Name: parts[2], Provider: parts[3], Version: parts[4]}, false)
		return nil
	})
}

func (m *ScanManager) worker() {
	defer m.wg.Done()
	for {
		select {
		case <-m.ctx.Done():
			return
		case job := <-m.queue:
			if m.metrics != nil {
				m.metrics.ScanQueueDepth.Add(-1)
				m.metrics.ScanRunning.Add(1)
			}
			m.running.Add(1)
			m.scan(job)
			m.running.Add(-1)
			if m.metrics != nil {
				m.metrics.ScanRunning.Add(-1)
			}
			m.clearPending(job.Digest)
		}
	}
}

func (m *ScanManager) scan(job ScanJob) {
	rec := recordFromJob(job)
	rec.Status = ScanScanning
	rec.StartedAt = time.Now().UTC()
	_ = m.repo.Save(rec, nil)

	ctx, cancel := context.WithTimeout(m.ctx, m.cfg.Timeout)
	defer cancel()
	raw, findings, dbUpdated, err := m.execute(ctx, job)
	rec.ScannerVersion = m.scannerVersion(job.Kind)
	rec.CompletedAt = time.Now().UTC()
	rec.DurationMS = rec.CompletedAt.Sub(rec.StartedAt).Milliseconds()
	if err != nil {
		if m.ctx.Err() != nil {
			rec.Status = ScanQueued
			rec.PolicyResult = ""
			rec.Message = "scan interrupted during shutdown"
			rec.CompletedAt = time.Time{}
			_ = m.repo.Save(rec, nil)
			return
		}
		rec.Status = ScanError
		rec.PolicyResult = PolicyDeny
		rec.Message = bounded(err.Error(), 4096)
		if m.metrics != nil {
			m.metrics.ScanErrorTotal.Add(1)
		}
	} else {
		rec.Findings = findings
		if job.Kind == ArtifactModule {
			_, rec.PassedChecks, rec.FailedChecks, rec.SkippedChecks, _ = parseCheckovReportDetailed(raw)
		}
		rec.DatabaseUpdated = dbUpdated
		rec.Status = ScanClean
		rec.PolicyResult = PolicyAllow
		for _, finding := range findings {
			if m.cfg.Deny[finding.Severity] {
				rec.PolicyResult = PolicyDeny
			}
		}
		if len(findings) > 0 {
			rec.Status = ScanFindings
		}
		if m.metrics != nil {
			m.metrics.ScanCompletedTotal.Add(1)
			m.metrics.AddScanFindings(findings)
		}
	}
	if saveErr := m.repo.Save(rec, raw); saveErr != nil {
		m.logger.Error("persist scan result failed", "digest", job.Digest, "error", saveErr)
		return
	}
	if m.webhooks != nil {
		event := "scan.completed"
		if rec.Status == ScanError {
			event = "scan.failed"
		}
		m.webhooks.Notify(event, WebhookPayload{Kind: string(job.Kind), Namespace: job.Namespace, Name: job.Name, Provider: job.Provider, Version: job.Version, Platform: job.Platform, Data: map[string]string{"digest": job.Digest, "status": string(rec.Status), "policy_result": string(rec.PolicyResult)}})
	}
}

func (m *ScanManager) scannerVersion(kind ArtifactKind) string {
	path := m.cfg.CheckovPath
	if kind == ArtifactProvider {
		path = m.cfg.TrivyPath
	}
	ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
	defer cancel()
	raw, err := runScannerCommand(ctx, path, []string{"--version"}, 4096, false)
	if err != nil {
		return "unknown"
	}
	line := strings.TrimSpace(strings.SplitN(string(raw), "\n", 2)[0])
	line = strings.TrimSpace(strings.TrimPrefix(line, "Version:"))
	if line == "" {
		return "unknown"
	}
	return bounded(line, 128)
}

func (m *ScanManager) scheduler() {
	defer m.wg.Done()
	ticker := time.NewTicker(m.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			records, err := m.repo.CurrentRecords()
			if err != nil {
				m.logger.Error("scheduled scan inventory failed", "error", err)
				continue
			}
			now := time.Now().UTC()
			for _, rec := range records {
				if rec.EffectiveStatus(now, m.cfg.StaleAfter) != ScanStale && rec.Status != ScanError {
					continue
				}
				job := ScanJob{Digest: rec.Digest, Kind: rec.Kind, ArtifactKey: rec.ArtifactKey, Namespace: rec.Namespace, Name: rec.Name, Provider: rec.Provider, Version: rec.Version, Platform: rec.Platform}
				if _, enqueueErr := m.Enqueue(job, true); enqueueErr != nil {
					m.logger.Warn("scheduled rescan enqueue failed", "digest", rec.Digest, "error", enqueueErr)
				}
			}
		}
	}
}

func (m *ScanManager) execute(ctx context.Context, job ScanJob) ([]byte, []Finding, time.Time, error) {
	artifactPath, err := m.store.resolve(job.ArtifactKey)
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	workspace, err := os.MkdirTemp("", "terraform-registry-scan-*")
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	defer func() { _ = os.RemoveAll(workspace) }()
	extractLimit := maxUploadSize
	if extractLimit < 1 {
		extractLimit = 500 << 20
	}
	if job.Kind == ArtifactProvider {
		if err := extractProviderForScan(artifactPath, workspace, extractLimit, maxExtractedFiles); err != nil {
			return nil, nil, time.Time{}, fmt.Errorf("extract provider: %w", err)
		}
		args := []string{"fs", "--format", "json", "--quiet", "--scanners", "vuln"}
		if m.cfg.TrivyCacheDir != "" {
			args = append(args, "--cache-dir", m.cfg.TrivyCacheDir)
		}
		if m.cfg.Offline {
			args = append(args, "--skip-db-update", "--offline-scan")
		}
		args = append(args, workspace)
		raw, err := runScannerCommand(ctx, m.cfg.TrivyPath, args, m.cfg.MaxReportSize, false)
		if err != nil {
			return raw, nil, time.Time{}, err
		}
		findings, dbUpdated, err := parseTrivyReport(raw)
		return raw, findings, dbUpdated, err
	}
	if err := extractModuleForScan(artifactPath, workspace, extractLimit, maxExtractedFiles); err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("extract module: %w", err)
	}
	args := []string{"-d", workspace, "--framework", "terraform", "-o", "json", "--quiet", "--compact"}
	raw, err := runScannerCommand(ctx, m.cfg.CheckovPath, args, m.cfg.MaxReportSize, true)
	if err != nil {
		return raw, nil, time.Time{}, err
	}
	findings, err := parseCheckovReport(raw)
	return raw, findings, time.Now().UTC(), err
}

func runScannerCommand(ctx context.Context, path string, args []string, maxOutput int64, allowFindingsExit bool) ([]byte, error) {
	cmd := exec.CommandContext(ctx, path, args...) // #nosec G204 -- executable path is operator configuration; argv is fixed and never shell-expanded.
	cmd.Env = scannerEnvironment()
	stdout := &cappedOutput{max: maxOutput}
	stderr := &cappedOutput{max: min(maxOutput, 1<<20)}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("scanner timed out: %w", ctx.Err())
	}
	if stdout.exceeded || stderr.exceeded {
		return nil, fmt.Errorf("scanner output exceeds configured limit")
	}
	data := append([]byte(nil), stdout.buf.Bytes()...)
	errorText := bounded(stderr.buf.String(), 4096)
	if err != nil {
		var exitErr *exec.ExitError
		if allowFindingsExit && errors.As(err, &exitErr) && exitErr.ExitCode() == 1 && len(data) > 0 {
			return data, nil
		}
		return data, fmt.Errorf("scanner failed: %w: %s", err, errorText)
	}
	return data, nil
}

func scannerEnvironment() []string {
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=/tmp", "LANG=C.UTF-8", "LC_ALL=C.UTF-8"}
	for _, key := range []string{"SSL_CERT_FILE", "SSL_CERT_DIR", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	return env
}

func (m *ScanManager) clearPending(digest string) {
	m.mu.Lock()
	delete(m.pending, digest)
	m.mu.Unlock()
}

func newScanID() string {
	var random [16]byte
	if _, err := rand.Read(random[:]); err == nil {
		return time.Now().UTC().Format("20060102T150405.000000000Z") + "-" + hex.EncodeToString(random[:])
	}
	return strings.ReplaceAll(time.Now().UTC().Format(time.RFC3339Nano), ":", "-")
}

func (m *ScanManager) Ready() error {
	if !m.cfg.Enabled {
		return nil
	}
	for name, path := range map[string]string{"trivy": m.cfg.TrivyPath, "checkov": m.cfg.CheckovPath} {
		if filepath.IsAbs(path) {
			if info, err := os.Stat(path); err != nil || info.IsDir() || info.Mode()&0111 == 0 {
				return fmt.Errorf("%s scanner unavailable", name)
			}
		} else if _, err := exec.LookPath(path); err != nil {
			return fmt.Errorf("%s scanner unavailable", name)
		}
	}
	return nil
}
