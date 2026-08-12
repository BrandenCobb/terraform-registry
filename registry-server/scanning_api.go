package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
)

const maxWaiverDuration = 365 * 24 * time.Hour

type WaiverRepository struct {
	path string
	mu   sync.RWMutex
}

func NewWaiverRepository(storageRoot string) (*WaiverRepository, error) {
	path := filepath.Join(storageRoot, "scans", "waivers.json")
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return nil, err
	}
	return &WaiverRepository{path: path}, nil
}

func (r *WaiverRepository) load() ([]Waiver, error) {
	data, err := os.ReadFile(r.path)
	if os.IsNotExist(err) {
		return []Waiver{}, nil
	}
	if err != nil {
		return nil, err
	}
	var waivers []Waiver
	if err := json.Unmarshal(data, &waivers); err != nil {
		return nil, err
	}
	return waivers, nil
}

func (r *WaiverRepository) save(waivers []Waiver) error {
	data, err := json.MarshalIndent(waivers, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteScan(r.path, data)
}

func (r *WaiverRepository) Create(waiver Waiver, now time.Time) (*Waiver, error) {
	waiver.Owner = bounded(waiver.Owner, 256)
	waiver.Reason = bounded(waiver.Reason, 4096)
	waiver.FindingID = bounded(waiver.FindingID, 256)
	waiver.CreatedBy = bounded(waiver.CreatedBy, 256)
	if !validDigest(waiver.Digest) || waiver.Owner == "" || waiver.Reason == "" {
		return nil, fmt.Errorf("digest, owner, and reason are required")
	}
	if !waiver.ExpiresAt.After(now) || waiver.ExpiresAt.Sub(now) > maxWaiverDuration {
		return nil, fmt.Errorf("waiver expiry must be within one year")
	}
	waiver.ID = newScanID()
	waiver.CreatedAt = now.UTC()
	waiver.ExpiresAt = waiver.ExpiresAt.UTC()
	r.mu.Lock()
	defer r.mu.Unlock()
	waivers, err := r.load()
	if err != nil {
		return nil, err
	}
	waivers = append(waivers, waiver)
	if err := r.save(waivers); err != nil {
		return nil, err
	}
	return &waiver, nil
}

func (r *WaiverRepository) Active(digest string, now time.Time) ([]*Waiver, error) {
	if !validDigest(digest) {
		return nil, fmt.Errorf("invalid digest")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	waivers, err := r.load()
	if err != nil {
		return nil, err
	}
	var active []*Waiver
	for i := range waivers {
		if waivers[i].Digest == digest && waivers[i].Active(now) {
			copy := waivers[i]
			active = append(active, &copy)
		}
	}
	return active, nil
}

func (r *WaiverRepository) List() ([]Waiver, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.load()
}

func (r *WaiverRepository) Delete(id string) error {
	if id == "" || strings.ContainsAny(id, "/\\") {
		return fmt.Errorf("invalid waiver id")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	waivers, err := r.load()
	if err != nil {
		return err
	}
	filtered := waivers[:0]
	found := false
	for _, waiver := range waivers {
		if waiver.ID == id {
			found = true
			continue
		}
		filtered = append(filtered, waiver)
	}
	if !found {
		return os.ErrNotExist
	}
	return r.save(filtered)
}

func scanAllowed(digest string) bool {
	if !scanConfig.Enabled || scanConfig.Mode == ScanModeVisibility {
		return true
	}
	if scanRepo == nil {
		return false
	}
	record, err := scanRepo.Current(digest)
	if err != nil {
		return false
	}
	var waivers []*Waiver
	if waiverRepo != nil {
		waivers, _ = waiverRepo.Active(digest, time.Now().UTC())
	}
	return policyAllows(scanConfig.Mode, record, waivers, time.Now().UTC(), scanConfig.StaleAfter)
}

func policyResultWithWaivers(record *ScanRecord, waivers []*Waiver, now time.Time) PolicyResult {
	if policyAllows(scanConfig.Mode, record, waivers, now, scanConfig.StaleAfter) {
		return PolicyAllow
	}
	return PolicyDeny
}

func activeWaiversByDigest(now time.Time) (map[string][]*Waiver, error) {
	byDigest := make(map[string][]*Waiver)
	if waiverRepo == nil {
		return byDigest, nil
	}
	waivers, err := waiverRepo.List()
	if err != nil {
		return nil, err
	}
	for i := range waivers {
		if !waivers[i].Active(now) {
			continue
		}
		waiver := waivers[i]
		byDigest[waiver.Digest] = append(byDigest[waiver.Digest], &waiver)
	}
	return byDigest, nil
}

func artifactPathScanAllowed(path string) bool {
	if !scanConfig.Enabled || scanConfig.Mode == ScanModeVisibility {
		return true
	}
	base := filepath.Base(path)
	if providerFileRE.MatchString(base) {
		stem := strings.TrimSuffix(base, ".zip")
		parts := strings.Split(stem, "_")
		return len(parts) > 0 && scanAllowed(parts[len(parts)-1])
	}
	if moduleFileRE.MatchString(base) {
		digest := strings.TrimSuffix(strings.TrimPrefix(base, "module_"), ".tar.gz")
		return scanAllowed(digest)
	}
	return true
}

func securityHealthHandler(w http.ResponseWriter, r *http.Request) {
	data := map[string]interface{}{"enabled": scanConfig.Enabled, "mode": scanConfig.Mode, "ready": true}
	if scanner != nil {
		if err := scanner.Ready(); err != nil {
			data["ready"] = false
			data["message"] = err.Error()
		}
	}
	if metrics != nil {
		data["queue_depth"] = metrics.ScanQueueDepth.Load()
		data["running"] = metrics.ScanRunning.Load()
	}
	writeJSON(w, http.StatusOK, APIResponse{Success: true, Data: data})
}

type SecurityInventorySummary struct {
	Enabled  bool          `json:"enabled"`
	Mode     ScanMode      `json:"mode"`
	Total    int           `json:"total"`
	Clean    int           `json:"clean"`
	Findings int           `json:"findings"`
	Active   int           `json:"active"`
	Unknown  int           `json:"unknown"`
	Blocked  int           `json:"blocked"`
	Counts   FindingCounts `json:"counts"`
}

func securitySummaryHandler(w http.ResponseWriter, r *http.Request) {
	summary := SecurityInventorySummary{Enabled: scanConfig.Enabled, Mode: scanConfig.Mode}
	if scanRepo == nil {
		writeJSON(w, http.StatusOK, APIResponse{Success: true, Data: summary})
		return
	}
	records, err := scanRepo.CurrentRecords()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Unable to summarize scans"})
		return
	}
	now := time.Now().UTC()
	waiversByDigest, err := activeWaiversByDigest(now)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Unable to summarize policy"})
		return
	}
	for _, record := range records {
		summary.Total++
		if policyResultWithWaivers(&record, waiversByDigest[record.Digest], now) == PolicyDeny {
			summary.Blocked++
		}
		scanSummary := record.Summary(now, scanConfig.StaleAfter)
		summary.Counts.Critical += scanSummary.Counts.Critical
		summary.Counts.High += scanSummary.Counts.High
		summary.Counts.Medium += scanSummary.Counts.Medium
		summary.Counts.Low += scanSummary.Counts.Low
		summary.Counts.Unknown += scanSummary.Counts.Unknown
		switch scanSummary.Status {
		case ScanClean:
			summary.Clean++
		case ScanFindings:
			summary.Findings++
		case ScanQueued, ScanScanning:
			summary.Active++
		default:
			summary.Unknown++
		}
	}
	writeJSON(w, http.StatusOK, APIResponse{Success: true, Data: summary})
}

func securityScansHandler(w http.ResponseWriter, r *http.Request) {
	limit, offset, ok := scanPagination(w, r)
	if !ok {
		return
	}
	if scanRepo == nil {
		writeJSON(w, http.StatusOK, APIResponse{Success: true, Data: []ScanRecord{}})
		return
	}
	records, err := scanRepo.CurrentRecords()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Unable to list scans"})
		return
	}
	kind, status, severity := r.URL.Query().Get("kind"), r.URL.Query().Get("status"), normalizeSeverity(r.URL.Query().Get("severity"))
	type overview struct {
		ScanRecord
		Summary ScanSummary `json:"summary"`
	}
	filtered := make([]overview, 0, len(records))
	now := time.Now().UTC()
	waiversByDigest, err := activeWaiversByDigest(now)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Unable to evaluate scan policy"})
		return
	}
	for _, record := range records {
		record.Status = record.EffectiveStatus(now, scanConfig.StaleAfter)
		record.PolicyResult = policyResultWithWaivers(&record, waiversByDigest[record.Digest], now)
		summary := record.Summary(now, scanConfig.StaleAfter)
		record.Findings = nil
		record.ArtifactKey = ""
		if kind != "" && string(record.Kind) != kind {
			continue
		}
		if status != "" && string(record.Status) != status {
			continue
		}
		if r.URL.Query().Get("severity") != "" && summary.HighestSeverity != severity {
			continue
		}
		filtered = append(filtered, overview{ScanRecord: record, Summary: summary})
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].ScanRecord.CompletedAt.After(filtered[j].ScanRecord.CompletedAt)
	})
	if offset > len(filtered) {
		offset = len(filtered)
	}
	end := offset + limit
	if end > len(filtered) {
		end = len(filtered)
	}
	writeJSON(w, http.StatusOK, APIResponse{Success: true, Data: map[string]interface{}{"items": filtered[offset:end], "total": len(filtered), "limit": limit, "offset": offset}})
}

func securityScanDetailHandler(w http.ResponseWriter, r *http.Request) {
	digest := mux.Vars(r)["digest"]
	if scanRepo == nil || !validDigest(digest) {
		writeJSON(w, http.StatusNotFound, APIResponse{Success: false, Message: "Scan not found"})
		return
	}
	record, err := scanRepo.Current(digest)
	if err != nil {
		writeJSON(w, http.StatusNotFound, APIResponse{Success: false, Message: "Scan not found"})
		return
	}
	now := time.Now().UTC()
	var waivers []*Waiver
	if waiverRepo != nil {
		waivers, _ = waiverRepo.Active(digest, now)
	}
	record.PolicyResult = policyResultWithWaivers(record, waivers, now)
	writeJSON(w, http.StatusOK, APIResponse{Success: true, Data: map[string]interface{}{"scan": record, "summary": record.Summary(now, scanConfig.StaleAfter), "waivers": waivers}})
}

func securityScanHistoryHandler(w http.ResponseWriter, r *http.Request) {
	limit, _, ok := scanPagination(w, r)
	if !ok {
		return
	}
	history, err := scanRepo.History(mux.Vars(r)["digest"], limit)
	if err != nil {
		writeJSON(w, http.StatusNotFound, APIResponse{Success: false, Message: "Scan history not found"})
		return
	}
	writeJSON(w, http.StatusOK, APIResponse{Success: true, Data: history})
}

func securityRawReportHandler(w http.ResponseWriter, r *http.Request) {
	digest, id := mux.Vars(r)["digest"], mux.Vars(r)["scanID"]
	raw, err := scanRepo.Raw(digest, id)
	if err != nil {
		httpJSONError(w, http.StatusNotFound, "Report not found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "scan-"+id+".json"))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// #nosec G705 -- authenticated raw scanner JSON is deliberately returned as a download, never embedded into HTML; attachment and nosniff headers prevent active rendering.
	_, _ = w.Write(raw)
}

func securityRescanHandler(w http.ResponseWriter, r *http.Request) {
	digest := mux.Vars(r)["digest"]
	record, err := scanRepo.Current(digest)
	if err != nil || scanner == nil {
		writeJSON(w, http.StatusNotFound, APIResponse{Success: false, Message: "Scan not found"})
		return
	}
	job := ScanJob{Digest: record.Digest, Kind: record.Kind, ArtifactKey: record.ArtifactKey, Namespace: record.Namespace, Name: record.Name, Provider: record.Provider, Version: record.Version, Platform: record.Platform}
	queued, err := scanner.Enqueue(job, true)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, APIResponse{Success: false, Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, APIResponse{Success: true, Data: map[string]bool{"queued": queued}})
}

func waiverCreateHandler(w http.ResponseWriter, r *http.Request) {
	var waiver Waiver
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&waiver); err != nil {
		writeJSON(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid waiver"})
		return
	}
	waiver.Digest = mux.Vars(r)["digest"]
	if keyStore != nil {
		if key := extractAPIKey(r); key != "" {
			if ak := keyStore.Validate(key); ak != nil {
				waiver.CreatedBy = ak.Name
			}
		}
	}
	created, err := waiverRepo.Create(waiver, time.Now().UTC())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, APIResponse{Success: true, Data: created})
}
func waiverDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if err := waiverRepo.Delete(mux.Vars(r)["waiverID"]); err != nil {
		writeJSON(w, http.StatusNotFound, APIResponse{Success: false, Message: "Waiver not found"})
		return
	}
	writeJSON(w, http.StatusOK, APIResponse{Success: true, Message: "Waiver deleted"})
}

func scanPagination(w http.ResponseWriter, r *http.Request) (int, int, bool) {
	limit, offset := 50, 0
	var err error
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 100 {
			writeJSON(w, http.StatusBadRequest, APIResponse{Success: false, Message: "limit must be 1..100"})
			return 0, 0, false
		}
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 || offset > 1000000 {
			writeJSON(w, http.StatusBadRequest, APIResponse{Success: false, Message: "invalid offset"})
			return 0, 0, false
		}
	}
	return limit, offset, true
}
