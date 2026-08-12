package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestQuarantineFiltersProviderAcrossProtocolRoutes(t *testing.T) {
	r, s, dir := setupTestEnv(t)
	defer cleanupTestEnv(dir)
	digest := string(bytes.Repeat([]byte{'d'}, 64))
	scanConfig = ScanConfig{Enabled: true, Mode: ScanModeQuarantine, StaleAfter: 24 * time.Hour}
	scanRepo, _ = NewScanRepository(dir)
	waiverRepo, _ = NewWaiverRepository(dir)
	seedProviderArtifact(t, s, "acme", "demo", "1.0.0", "linux", "amd64", digest)
	if err := scanRepo.Save(&ScanRecord{ID: newScanID(), Digest: digest, Kind: ArtifactProvider, Status: ScanFindings, PolicyResult: PolicyDeny, CompletedAt: time.Now().UTC()}, nil); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/v1/providers/acme/demo/versions",
		"/v1/providers/acme/demo/1.0.0/download/linux/amd64",
		"/registry.terraform.io/acme/demo/index.json",
		"/registry.terraform.io/acme/demo/1.0.0.json",
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusNotFound {
			t.Errorf("%s: expected 404, got %d: %s", path, w.Code, w.Body.String())
		}
	}
}

func TestVisibilityNeverFiltersDeniedArtifact(t *testing.T) {
	r, s, dir := setupTestEnv(t)
	defer cleanupTestEnv(dir)
	digest := string(bytes.Repeat([]byte{'e'}, 64))
	scanConfig = ScanConfig{Enabled: true, Mode: ScanModeVisibility, StaleAfter: time.Hour}
	scanRepo, _ = NewScanRepository(dir)
	waiverRepo, _ = NewWaiverRepository(dir)
	seedProviderArtifact(t, s, "acme", "demo", "1.0.0", "linux", "amd64", digest)
	_ = scanRepo.Save(&ScanRecord{ID: newScanID(), Digest: digest, Kind: ArtifactProvider, Status: ScanFindings, PolicyResult: PolicyDeny, CompletedAt: time.Now().UTC()}, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/providers/acme/demo/versions", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestQuarantineFiltersModuleExactAndLatest(t *testing.T) {
	r, s, dir := setupTestEnv(t)
	defer cleanupTestEnv(dir)
	digest := string(bytes.Repeat([]byte{'f'}, 64))
	scanConfig = ScanConfig{Enabled: true, Mode: ScanModeQuarantine, StaleAfter: time.Hour}
	scanRepo, _ = NewScanRepository(dir)
	waiverRepo, _ = NewWaiverRepository(dir)
	_ = s.AddModuleVersion("acme", "vpc", "aws", "1.0.0")
	filename := "module_" + digest + ".tar.gz"
	_ = s.Put("modules/acme/vpc/aws/1.0.0/"+filename, []byte("x"))
	meta, _ := json.Marshal(ModuleArtifactMeta{Filename: filename, SHA256: digest})
	_ = s.Put("modules/acme/vpc/aws/1.0.0/artifact.json", meta)
	_ = scanRepo.Save(&ScanRecord{ID: newScanID(), Digest: digest, Kind: ArtifactModule, Status: ScanError, PolicyResult: PolicyDeny, CompletedAt: time.Now().UTC()}, nil)
	for _, path := range []string{"/v1/modules/acme/vpc/aws/versions", "/v1/modules/acme/vpc/aws/1.0.0/download", "/v1/modules/acme/vpc/aws/download"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusNotFound {
			t.Errorf("%s expected 404 got %d", path, w.Code)
		}
	}
}

func TestSecurityAPIListsDetailsHistoryRawAndRescan(t *testing.T) {
	r, _, dir := setupTestEnv(t)
	defer cleanupTestEnv(dir)
	digest := string(bytes.Repeat([]byte{'a'}, 64))
	scanConfig = ScanConfig{Enabled: true, Mode: ScanModeVisibility, StaleAfter: time.Hour}
	scanRepo, _ = NewScanRepository(dir)
	waiverRepo, _ = NewWaiverRepository(dir)
	rec := &ScanRecord{ID: "scan-1", Digest: digest, Kind: ArtifactProvider, Namespace: "acme", Name: "demo", Version: "1.0.0", Platform: "linux/amd64", Scanner: "trivy", Status: ScanFindings, PolicyResult: PolicyDeny, Findings: []Finding{{ID: "CVE-X", Severity: SeverityCritical}}, QueuedAt: time.Now().UTC(), CompletedAt: time.Now().UTC()}
	_ = scanRepo.Save(rec, []byte(`{"Results":[]}`))
	for _, path := range []string{"/api/v1/security/scans?limit=10", "/api/v1/security/scans/" + digest, "/api/v1/security/scans/" + digest + "/history?limit=10", "/api/v1/security/scans/" + digest + "/reports/scan-1"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK {
			t.Errorf("%s expected 200 got %d: %s", path, w.Code, w.Body.String())
		}
	}
}

func TestSecurityOverviewRedactsDetailsButRetainsCountsAndFilters(t *testing.T) {
	r, _, dir := setupTestEnv(t)
	defer cleanupTestEnv(dir)
	digest := string(bytes.Repeat([]byte{'9'}, 64))
	scanConfig = ScanConfig{Enabled: true, Mode: ScanModeVisibility, StaleAfter: time.Hour}
	scanRepo, _ = NewScanRepository(dir)
	rec := &ScanRecord{ID: "scan-public", Digest: digest, Kind: ArtifactProvider, ArtifactKey: "providers/private/path.zip", Status: ScanFindings, PolicyResult: PolicyDeny, CompletedAt: time.Now().UTC(), Findings: []Finding{{ID: "CVE-SECRET", Title: "private package detail", Severity: SeverityCritical}}}
	if err := scanRepo.Save(rec, []byte(`{"private":"raw"}`)); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/security/scans?severity=critical", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d: %s", w.Code, w.Body.String())
	}
	var response struct {
		Data struct {
			Items []struct {
				ArtifactKey string      `json:"artifact_key"`
				Findings    []Finding   `json:"findings"`
				Summary     ScanSummary `json:"summary"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data.Items) != 1 {
		t.Fatalf("expected severity-filtered item, got %d", len(response.Data.Items))
	}
	item := response.Data.Items[0]
	if item.ArtifactKey != "" || len(item.Findings) != 0 {
		t.Fatalf("public overview leaked details: %+v", item)
	}
	if item.Summary.Counts.Critical != 1 {
		t.Fatalf("expected aggregate count, got %+v", item.Summary.Counts)
	}
}

func TestSecuritySummaryAggregatesCompleteInventory(t *testing.T) {
	r, _, dir := setupTestEnv(t)
	defer cleanupTestEnv(dir)
	scanConfig = ScanConfig{Enabled: true, Mode: ScanModeEnforce, StaleAfter: time.Hour}
	scanRepo, _ = NewScanRepository(dir)
	now := time.Now().UTC()
	for i, record := range []*ScanRecord{
		{Digest: string(bytes.Repeat([]byte{'1'}, 64)), Kind: ArtifactProvider, Status: ScanClean, PolicyResult: PolicyAllow, CompletedAt: now},
		{Digest: string(bytes.Repeat([]byte{'2'}, 64)), Kind: ArtifactModule, Status: ScanFindings, PolicyResult: PolicyDeny, CompletedAt: now, Findings: []Finding{{ID: "critical", Severity: SeverityCritical}, {ID: "low", Severity: SeverityLow}}},
		{Digest: string(bytes.Repeat([]byte{'3'}, 64)), Kind: ArtifactProvider, Status: ScanScanning, PolicyResult: PolicyDeny},
	} {
		record.ID = fmt.Sprintf("summary-%d", i)
		if err := scanRepo.Save(record, nil); err != nil {
			t.Fatal(err)
		}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/security/summary", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d: %s", w.Code, w.Body.String())
	}
	var response struct {
		Data SecurityInventorySummary `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.Total != 3 || response.Data.Clean != 1 || response.Data.Findings != 1 || response.Data.Active != 1 || response.Data.Blocked != 2 {
		t.Fatalf("unexpected summary: %+v", response.Data)
	}
	if response.Data.Counts.Critical != 1 || response.Data.Counts.Low != 1 {
		t.Fatalf("unexpected finding counts: %+v", response.Data.Counts)
	}
	if response.Data.Mode != ScanModeEnforce || !response.Data.Enabled {
		t.Fatalf("missing policy context: %+v", response.Data)
	}
}

func TestSecuritySummaryUsesLivePolicy(t *testing.T) {
	r, _, dir := setupTestEnv(t)
	defer cleanupTestEnv(dir)
	digest := string(bytes.Repeat([]byte{'4'}, 64))
	scanConfig = ScanConfig{Enabled: true, Mode: ScanModeVisibility, StaleAfter: time.Hour}
	scanRepo, _ = NewScanRepository(dir)
	waiverRepo, _ = NewWaiverRepository(dir)
	record := &ScanRecord{ID: "live-policy", Digest: digest, Kind: ArtifactProvider, Status: ScanFindings, PolicyResult: PolicyDeny, CompletedAt: time.Now().UTC(), Findings: []Finding{{ID: "critical", Severity: SeverityCritical}}}
	if err := scanRepo.Save(record, nil); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/security/summary", nil))
	var response struct {
		Data SecurityInventorySummary `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.Blocked != 0 {
		t.Fatalf("visibility mode must report live allow policy, got %+v", response.Data)
	}
}

func TestSecurityOverviewReportsLivePolicy(t *testing.T) {
	r, _, dir := setupTestEnv(t)
	defer cleanupTestEnv(dir)
	digest := string(bytes.Repeat([]byte{'5'}, 64))
	scanConfig = ScanConfig{Enabled: true, Mode: ScanModeVisibility, StaleAfter: time.Hour}
	scanRepo, _ = NewScanRepository(dir)
	waiverRepo, _ = NewWaiverRepository(dir)
	if err := scanRepo.Save(&ScanRecord{ID: "overview-policy", Digest: digest, Kind: ArtifactProvider, Status: ScanFindings, PolicyResult: PolicyDeny, CompletedAt: time.Now().UTC(), Findings: []Finding{{ID: "critical", Severity: SeverityCritical}}}, nil); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/security/scans", nil))
	var response struct {
		Data struct {
			Items []ScanRecord `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data.Items) != 1 || response.Data.Items[0].PolicyResult != PolicyAllow {
		t.Fatalf("overview must expose effective policy, got %+v", response.Data.Items)
	}
}

func TestSecurityDetailReportsLivePolicy(t *testing.T) {
	r, _, dir := setupTestEnv(t)
	defer cleanupTestEnv(dir)
	digest := string(bytes.Repeat([]byte{'6'}, 64))
	scanConfig = ScanConfig{Enabled: true, Mode: ScanModeVisibility, StaleAfter: time.Hour}
	scanRepo, _ = NewScanRepository(dir)
	waiverRepo, _ = NewWaiverRepository(dir)
	if err := scanRepo.Save(&ScanRecord{ID: "detail-policy", Digest: digest, Kind: ArtifactProvider, Status: ScanFindings, PolicyResult: PolicyDeny, CompletedAt: time.Now().UTC()}, nil); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/security/scans/"+digest, nil))
	var response struct {
		Data struct {
			Scan ScanRecord `json:"scan"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.Scan.PolicyResult != PolicyAllow {
		t.Fatalf("detail must expose effective policy, got %+v", response.Data.Scan)
	}
}

func TestWaiverRepositoryValidationAndExpiry(t *testing.T) {
	repo, _ := NewWaiverRepository(t.TempDir())
	now := time.Now().UTC()
	if _, err := repo.Create(Waiver{Digest: string(bytes.Repeat([]byte{'b'}, 64)), Owner: "", Reason: "because", ExpiresAt: now.Add(time.Hour)}, now); err == nil {
		t.Fatal("expected owner validation")
	}
	w, err := repo.Create(Waiver{Digest: string(bytes.Repeat([]byte{'b'}, 64)), Owner: "security", Reason: "accepted for migration", ExpiresAt: now.Add(time.Hour)}, now)
	if err != nil {
		t.Fatal(err)
	}
	active, _ := repo.Active(w.Digest, now)
	if len(active) != 1 {
		t.Fatal("expected active waiver")
	}
	active, _ = repo.Active(w.Digest, now.Add(2*time.Hour))
	if len(active) != 0 {
		t.Fatal("expired waiver active")
	}
	if err := repo.Delete(w.ID); err != nil {
		t.Fatal(err)
	}
}

func seedProviderArtifact(t *testing.T, s *Store, ns, name, version, osName, arch, digest string) {
	t.Helper()
	_ = s.AddProviderVersion(ns, name, version)
	filename := "terraform-provider-" + name + "_" + version + "_" + osName + "_" + arch + "_" + digest + ".zip"
	meta, _ := json.Marshal(PlatformMeta{OS: osName, Arch: arch, Filename: filename, Shasum: digest, Protocols: []string{"5.0"}})
	_ = s.Put("providers/"+ns+"/"+name+"/"+version+"/"+osName+"_"+arch+".json", meta)
	_ = s.Put("providers/"+ns+"/"+name+"/"+version+"/"+filename, []byte("x"))
}
func cleanupTestEnv(dir string) {
	scanConfig = ScanConfig{}
	scanRepo = nil
	waiverRepo = nil
	scanner = nil
	_ = dir
}
