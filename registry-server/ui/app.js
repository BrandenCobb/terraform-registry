// Terraform Registry Enterprise Command Center
(function () {
    'use strict';

    const API_BASE = '/api/v1';
    const state = { securityAPIKey: '', scans: [], providers: [], modules: [], health: null, summary: null, securityStatus: '', refreshTimer: null };
    const $ = id => document.getElementById(id);

    function element(tag, className, text) {
        const node = document.createElement(tag);
        if (className) node.className = className;
        if (text !== undefined && text !== null) node.textContent = String(text);
        return node;
    }
    function clear(node) { node.replaceChildren(); }
    function formatNumber(value) { return Number(value || 0).toLocaleString(); }
    function relativeTime(value) {
        if (!value) return 'Never completed';
        const seconds = Math.round((Date.now() - new Date(value).getTime()) / 1000);
        if (seconds < 60) return `${Math.max(0, seconds)}s ago`;
        if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
        if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
        return `${Math.floor(seconds / 86400)}d ago`;
    }
    function identity(record) { return [record.namespace, record.name, record.provider].filter(Boolean).join('/') || record.kind || 'artifact'; }
    function totalCounts(counts) { return Object.values(counts || {}).reduce((sum, value) => sum + Number(value || 0), 0); }
    function toast(message, kind) {
        const node = element('div', `toast ${kind || ''}`, message);
        $('toast-region').appendChild(node);
        window.setTimeout(() => node.remove(), 4200);
    }
    async function api(path, options) {
        try {
            const response = await fetch(API_BASE + path, options);
            const data = await response.json().catch(() => ({ success: false, message: `HTTP ${response.status}` }));
            if (!response.ok && data.success !== false) data.success = false;
            return data;
        } catch (error) { return { success: false, message: error.message }; }
    }

    function switchTab(tab, updateHash) {
        const target = $(`tab-${tab}`) ? tab : 'dashboard';
        document.querySelectorAll('[data-tab]').forEach(link => link.classList.toggle('active', link.dataset.tab === target));
        document.querySelectorAll('.tab-content').forEach(section => section.classList.toggle('active', section.id === `tab-${target}`));
        if (updateHash !== false) history.replaceState(null, '', `#${target}`);
        if (target === 'dashboard') loadDashboard();
        if (target === 'providers') loadProviders();
        if (target === 'modules') loadModules();
        if (target === 'security') loadSecurity();
        if (target === 'upload') toggleUploadFields();
    }

    async function loadDashboard() {
        const [stats, scans, health, summary] = await Promise.all([api('/stats'), api('/security/scans?limit=100'), api('/security/health'), api('/security/summary')]);
        if (stats.success) {
            $('stat-providers').textContent = formatNumber(stats.data.providers);
            $('stat-provider-versions').textContent = `${formatNumber(stats.data.provider_versions)} versions`;
            $('stat-modules').textContent = formatNumber(stats.data.modules);
            $('stat-module-versions').textContent = `${formatNumber(stats.data.module_versions)} versions`;
        }
        $('registry-url').textContent = window.location.origin;
        state.health = health.success ? health.data : null;
        state.summary = summary.success ? summary.data : null;
        state.scans = scans.success && scans.data ? scans.data.items || [] : [];
        updateSystemStatus();
        renderDashboardSecurity();
    }

    function updateSystemStatus() {
        const pulse = $('system-pulse');
        pulse.className = 'status-dot';
        if (!state.health) { $('system-label').textContent = 'Unavailable'; pulse.classList.add('error'); return; }
        if (!state.health.enabled) { $('system-label').textContent = 'Scanning off'; return; }
        if (state.health.ready) { $('system-label').textContent = `${state.health.mode} · ready`; pulse.classList.add('ready'); }
        else { $('system-label').textContent = 'Scanner degraded'; pulse.classList.add('error'); }
    }

    function inventory(scans) {
        const result = { clean: 0, blocked: 0, findings: 0, active: 0, unknown: 0, critical: 0, high: 0, medium: 0, low: 0 };
        scans.forEach(scan => {
            const counts = scan.summary && scan.summary.counts || {};
            ['critical', 'high', 'medium', 'low'].forEach(key => result[key] += Number(counts[key] || 0));
            if (scan.policy_result === 'deny') result.blocked++;
            if (scan.status === 'clean') result.clean++;
            else if (scan.status === 'findings') result.findings++;
            else if (scan.status === 'queued' || scan.status === 'scanning') result.active++;
            else result.unknown++;
        });
        return result;
    }

    function riskScore(inv, total) {
        if (!total) return 0;
        const penalty = inv.blocked * 22 + inv.critical * 15 + inv.high * 8 + inv.medium * 2 + inv.unknown * 4;
        return Math.max(0, Math.min(100, Math.round(100 - penalty / Math.max(1, total))));
    }

    function renderDashboardSecurity() {
        const inv = state.summary ? { clean: state.summary.clean, blocked: state.summary.blocked, findings: state.summary.findings, active: state.summary.active, unknown: state.summary.unknown, ...state.summary.counts } : inventory(state.scans);
        $('stat-clean').textContent = formatNumber(inv.clean);
        $('stat-blocked').textContent = formatNumber(inv.blocked);
        const inventoryTotal = state.summary ? state.summary.total : state.scans.length;
        const score = riskScore(inv, inventoryTotal);
        $('posture-score').textContent = inventoryTotal ? score : '—';
        document.querySelector('.orbit-ring').style.setProperty('--score', `${score}%`);
        $('posture-caption').textContent = !inventoryTotal ? 'No scan inventory yet' : score >= 90 ? 'Strong security posture' : score >= 70 ? 'Review recommended' : 'Immediate attention required';
        const alertCount = inv.blocked + inv.unknown;
        $('nav-alert-count').hidden = alertCount === 0;
        $('nav-alert-count').textContent = formatNumber(alertCount);
        renderRiskBars(inv);
        const health = state.health;
        $('scanner-line').textContent = health ? `${health.enabled ? 'Scanning enabled' : 'Scanning disabled'} · ${health.mode || 'visibility'} mode · ${formatNumber(health.queue_depth)} queued · ${formatNumber(health.running)} running` : 'Scanner status unavailable';
    }

    function renderRiskBars(inv) {
        const rows = [['Critical', inv.critical, 'var(--red)'], ['High', inv.high, 'var(--coral)'], ['Medium', inv.medium, 'var(--amber)'], ['Low', inv.low, 'var(--blue)']];
        const maximum = Math.max(1, ...rows.map(row => row[1]));
        const container = $('risk-bars'); clear(container);
        rows.forEach(([label, value, color]) => {
            const row = element('div', 'risk-row'); row.appendChild(element('span', '', label));
            const track = element('div', 'bar-track'); const fill = element('div', 'bar-fill');
            fill.style.setProperty('--width', `${value / maximum * 100}%`); fill.style.setProperty('--bar', color); track.appendChild(fill);
            row.appendChild(track); row.appendChild(element('b', '', formatNumber(value))); container.appendChild(row);
        });
    }

    function renderArtifactCard(item, kind) {
        const card = element('button', 'artifact-card'); card.type = 'button';
        const left = element('div'); const name = kind === 'provider' ? `${item.namespace}/${item.name}` : `${item.namespace}/${item.name}/${item.provider}`;
        left.appendChild(element('div', 'artifact-name', name));
        left.appendChild(element('div', 'artifact-meta', `${formatNumber((item.versions || []).length)} version${(item.versions || []).length === 1 ? '' : 's'}`));
        const versions = element('div', 'version-stack');
        (item.versions || []).slice(-4).reverse().forEach(version => versions.appendChild(element('span', 'version-badge', version.version)));
        left.appendChild(versions); card.appendChild(left); card.appendChild(element('span', 'artifact-arrow', '↗'));
        card.addEventListener('click', () => kind === 'provider' ? showProviderDetail(item.namespace, item.name) : showModuleDetail(item.namespace, item.name, item.provider));
        return card;
    }

    function renderArtifacts(kind) {
        const data = kind === 'provider' ? state.providers : state.modules;
        const query = $(kind === 'provider' ? 'provider-search' : 'module-search').value.trim().toLowerCase();
        const container = $(kind === 'provider' ? 'providers-list' : 'modules-list'); clear(container);
        const filtered = data.filter(item => Object.values(item).some(value => typeof value === 'string' && value.toLowerCase().includes(query)));
        if (!filtered.length) { container.appendChild(element('div', 'empty-state', query ? 'No artifacts match this filter.' : `No ${kind}s published yet.`)); return; }
        filtered.forEach(item => container.appendChild(renderArtifactCard(item, kind)));
    }
    async function loadProviders() { const response = await api('/providers'); state.providers = response.success && Array.isArray(response.data) ? response.data : []; renderArtifacts('provider'); }
    async function loadModules() { const response = await api('/modules'); state.modules = response.success && Array.isArray(response.data) ? response.data : []; renderArtifacts('module'); }

    const presentations = {
        findings: ['Warning / findings', 'var(--amber)'], clean: ['Verified clean', 'var(--green)'], queued: ['Queued', 'var(--blue)'],
        scanning: ['Scanning', 'var(--cyan)'], error: ['Scan error', 'var(--red)'], stale: ['Stale', 'var(--muted)'], disabled: ['Unknown', 'var(--muted)']
    };
    function presentation(scan) { return scan.policy_result === 'deny' ? ['Policy blocked', 'var(--red)'] : presentations[scan.status] || ['Unknown', 'var(--muted)']; }

    async function loadSecurity() {
        const [response, summary] = await Promise.all([api('/security/scans?limit=100'), api('/security/summary')]);
        if (!response.success) { clear($('security-list')); $('security-list').appendChild(element('div', 'empty-state', response.message || 'Unable to load scans.')); return; }
        state.scans = response.data.items || [];
        if (summary.success) state.summary = summary.data;
        const health = await api('/security/health'); if (health.success) { state.health = health.data; updateSystemStatus(); }
        renderSecurity(); renderDashboardSecurity();
    }

    function filteredScans() {
        const search = $('security-search').value.trim().toLowerCase(); const kind = $('security-kind').value; const severity = $('security-severity').value.toLowerCase();
        return state.scans.filter(scan => {
            const effectiveStatus = state.securityStatus === 'scanning' ? ['queued', 'scanning'].includes(scan.status) : !state.securityStatus || scan.status === state.securityStatus;
            const matchesKind = !kind || scan.kind === kind;
            const counts = scan.summary && scan.summary.counts || {}; const matchesSeverity = !severity || Number(counts[severity] || 0) > 0;
            const haystack = `${identity(scan)} ${scan.digest} ${scan.platform || ''} ${scan.scanner || ''}`.toLowerCase();
            return effectiveStatus && matchesKind && matchesSeverity && (!search || haystack.includes(search));
        });
    }

    function renderSecurity() {
        const inv = state.summary ? { clean: state.summary.clean, blocked: state.summary.blocked, findings: state.summary.findings, active: state.summary.active, unknown: state.summary.unknown, ...state.summary.counts } : inventory(state.scans);
        const inventoryTotal = state.summary ? state.summary.total : state.scans.length; const score = riskScore(inv, inventoryTotal);
        $('security-risk-score').textContent = inventoryTotal ? score : '—';
        $('security-risk-label').textContent = score >= 90 ? 'Strong' : score >= 70 ? 'Guarded' : 'At risk';
        const stats = $('security-stats'); clear(stats);
        [['Blocked', inv.blocked, 'var(--red)'], ['Findings', inv.findings, 'var(--amber)'], ['Clean', inv.clean, 'var(--green)'], ['Active / unknown', inv.active + inv.unknown, 'var(--blue)']].forEach(([label, value, color]) => {
            const card = element('article', 'stat-card'); card.style.setProperty('--card-accent', color); card.appendChild(element('div', 'stat-label', label)); card.appendChild(element('div', 'stat-value', formatNumber(value))); stats.appendChild(card);
        });
        const list = $('security-list'); clear(list); const scans = filteredScans();
        if (!scans.length) { list.appendChild(element('div', 'empty-state', 'No scan records match these controls.')); return; }
        scans.forEach(scan => list.appendChild(scanCard(scan)));
    }

    function scanCard(scan) {
        const [label, color] = presentation(scan); const card = element('button', 'artifact-card security-card'); card.type = 'button'; card.style.setProperty('--status-color', color);
        const info = element('div', 'scan-identity'); info.appendChild(element('div', 'artifact-name', `${identity(scan)} ${scan.version || ''}`.trim()));
        info.appendChild(element('div', 'artifact-meta', `${scan.platform || scan.kind} · ${scan.scanner || 'scanner'}`)); info.appendChild(element('div', 'digest', `${scan.digest.slice(0, 18)}…`));
        const status = element('div', 'scan-status-stack'); status.appendChild(element('span', 'scan-badge', label)); status.appendChild(element('span', 'scan-timing', relativeTime(scan.completed_at)));
        const pills = element('div', 'severity-pills'); const counts = scan.summary && scan.summary.counts || {};
        [['critical', 'C'], ['high', 'H'], ['medium', 'M'], ['low', 'L']].forEach(([key, short]) => pills.appendChild(element('span', `severity-pill ${key}`, `${short} ${formatNumber(counts[key])}`)));
        card.append(info, status, pills, element('span', 'artifact-arrow', '›')); card.addEventListener('click', () => showScanDetail(scan.digest)); return card;
    }

    function promptAPIKey(message) {
        if (!state.securityAPIKey) state.securityAPIKey = window.prompt(message || 'Read-capable API key:') || '';
        return state.securityAPIKey;
    }
    async function showScanDetail(digest) {
        if (!promptAPIKey('Read-capable API key for security details:')) return;
        const headers = { 'X-API-Key': state.securityAPIKey };
        const [response, historyResponse] = await Promise.all([
            api(`/security/scans/${encodeURIComponent(digest)}`, { headers }),
            api(`/security/scans/${encodeURIComponent(digest)}/history?limit=10`, { headers })
        ]);
        if (!response.success) { state.securityAPIKey = ''; toast(response.message || 'Unable to load details.', 'error'); return; }
        const scan = response.data.scan; const summary = response.data.summary || {}; const body = $('modal-body'); clear(body);
        const title = element('div', 'modal-title', `${identity(scan)} ${scan.version || ''}`.trim()); title.id = 'modal-title'; body.appendChild(title);
        body.appendChild(element('div', 'modal-subtitle', `sha256:${scan.digest} · ${scan.scanner || 'scanner'} ${scan.scanner_version || ''} · ${relativeTime(scan.completed_at)}`));
        const metrics = element('div', 'modal-summary'); const counts = summary.counts || {};
        [['Critical', counts.critical], ['High', counts.high], ['Medium', counts.medium], ['Low', counts.low]].forEach(([label, value]) => { const metric = element('div', 'modal-metric'); metric.append(element('strong', '', formatNumber(value)), element('span', '', label)); metrics.appendChild(metric); }); body.appendChild(metrics);
        const table = element('div', 'findings-table');
        (scan.findings || []).forEach(finding => { const row = element('article', `finding-row severity-${String(finding.severity || 'unknown').toLowerCase()}`); row.append(element('strong', '', `${finding.severity || 'UNKNOWN'} · ${finding.id || 'Finding'}`), element('div', '', finding.title || finding.description || 'No description'), element('small', 'artifact-meta', [finding.file, finding.start_line && `line ${finding.start_line}`, finding.resource || finding.package, finding.fixed_version && `fix ${finding.fixed_version}`].filter(Boolean).join(' · '))); table.appendChild(row); });
        if (!(scan.findings || []).length) table.appendChild(element('div', 'empty-state', 'No normalized findings.')); body.appendChild(table);
        renderWaivers(body, digest, response.data.waivers || []);
        renderScanHistory(body, historyResponse.success ? historyResponse.data || [] : []);
        const actions = element('div', 'security-actions'); const raw = element('button', 'btn', 'Download raw JSON'); raw.type = 'button'; raw.addEventListener('click', () => downloadRawReport(digest, scan.id)); const rescan = element('button', 'btn btn-primary', 'Queue rescan →'); rescan.type = 'button'; rescan.addEventListener('click', () => requestRescan(digest)); actions.append(raw, rescan); body.appendChild(actions); openModal();
    }

    function renderWaivers(body, digest, waivers) {
        const heading = element('div', 'detail-section-heading'); heading.append(element('h3', '', 'Active waivers'));
        const create = element('button', 'btn', '+ Create waiver'); create.type = 'button'; create.addEventListener('click', () => createWaiver(digest)); heading.appendChild(create); body.appendChild(heading);
        const list = element('div', 'waiver-list');
        if (!waivers.length) list.appendChild(element('div', 'detail-empty', 'No active policy waivers.'));
        waivers.forEach(waiver => {
            const row = element('div', 'waiver-row'); const copy = element('div'); copy.append(element('strong', '', waiver.owner), element('span', '', waiver.reason), element('small', '', `Expires ${new Date(waiver.expires_at).toLocaleString()} · ${waiver.created_by || 'unknown actor'}`));
            const remove = element('button', 'btn btn-danger', 'Revoke'); remove.type = 'button'; remove.addEventListener('click', () => revokeWaiver(waiver.id, digest)); row.append(copy, remove); list.appendChild(row);
        }); body.appendChild(list);
    }

    function renderScanHistory(body, history) {
        const heading = element('div', 'detail-section-heading'); heading.appendChild(element('h3', '', 'Scan history')); body.appendChild(heading);
        const timeline = element('div', 'scan-history');
        if (!history.length) timeline.appendChild(element('div', 'detail-empty', 'No historical scans retained.'));
        history.forEach(scan => { const [label, color] = presentation(scan); const row = element('div', 'history-row'); row.style.setProperty('--status-color', color); row.append(element('span', 'history-dot'), element('strong', '', label), element('span', '', `${scan.scanner || 'scanner'} ${scan.scanner_version || ''}`), element('time', '', relativeTime(scan.completed_at || scan.queued_at))); timeline.appendChild(row); }); body.appendChild(timeline);
    }

    async function createWaiver(digest) {
        const key = window.prompt('Admin API key:'); if (!key) return;
        const owner = window.prompt('Waiver owner or accountable team:'); if (!owner) return;
        const reason = window.prompt('Business justification:'); if (!reason) return;
        const hours = Number(window.prompt('Duration in hours (maximum 8760):', '24')); if (!Number.isFinite(hours) || hours <= 0 || hours > 8760) { toast('Invalid waiver duration.', 'error'); return; }
        const response = await api(`/security/scans/${encodeURIComponent(digest)}/waivers`, { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-API-Key': key }, body: JSON.stringify({ owner, reason, expires_at: new Date(Date.now() + hours * 3600000).toISOString() }) });
        toast(response.success ? 'Policy waiver created.' : response.message || 'Unable to create waiver.', response.success ? '' : 'error'); if (response.success) { closeModal(); loadSecurity(); }
    }

    async function revokeWaiver(id, digest) {
        if (!window.confirm('Revoke this waiver immediately?')) return; const key = window.prompt('Admin API key:'); if (!key) return;
        const response = await api(`/security/waivers/${encodeURIComponent(id)}`, { method: 'DELETE', headers: { 'X-API-Key': key } });
        toast(response.success ? 'Waiver revoked.' : response.message || 'Unable to revoke waiver.', response.success ? '' : 'error'); if (response.success) { closeModal(); showScanDetail(digest); }
    }

    async function downloadRawReport(digest, scanID) {
        const response = await fetch(`${API_BASE}/security/scans/${encodeURIComponent(digest)}/reports/${encodeURIComponent(scanID)}`, { headers: { 'X-API-Key': state.securityAPIKey } });
        if (!response.ok) { toast('Unable to download raw report.', 'error'); return; }
        const blob = await response.blob(); const url = URL.createObjectURL(blob); const link = element('a'); link.href = url; link.download = `scan-${scanID}.json`; link.click(); URL.revokeObjectURL(url);
    }
    async function requestRescan(digest) {
        const key = window.prompt('Write-capable API key:'); if (!key) return;
        const response = await api(`/security/scans/${encodeURIComponent(digest)}/rescan`, { method: 'POST', headers: { 'X-API-Key': key } });
        toast(response.success ? 'Rescan queued.' : response.message || 'Unable to queue rescan.', response.success ? '' : 'error'); if (response.success) { closeModal(); loadSecurity(); }
    }

    function versionItem(version, deleteAction, platforms) {
        const row = element('div', 'version-item'); row.appendChild(element('span', 'version-badge', version.version));
        if (platforms) { const list = element('div', 'platform-list'); (version.platforms || []).forEach(platform => list.appendChild(element('span', 'platform-badge', `${platform.os}/${platform.arch}`))); row.appendChild(list); }
        const remove = element('button', 'btn btn-danger', 'Delete'); remove.type = 'button'; remove.addEventListener('click', deleteAction); row.appendChild(remove); return row;
    }
    async function showProviderDetail(namespace, name) {
        const response = await api(`/providers/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}`); if (!response.success) return;
        const provider = response.data; const body = $('modal-body'); clear(body); const title = element('div', 'modal-title', `${provider.namespace}/${provider.name}`); title.id = 'modal-title'; body.append(title, element('p', 'artifact-meta', `Terraform provider · ${formatNumber(provider.versions.length)} versions`));
        const list = element('div', 'version-list'); provider.versions.forEach(version => list.appendChild(versionItem(version, () => deleteProvider(namespace, name, version.version), true))); body.appendChild(list); openModal();
    }
    async function showModuleDetail(namespace, name, providerName) {
        const response = await api(`/modules/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}/${encodeURIComponent(providerName)}`); if (!response.success) return;
        const module = response.data; const body = $('modal-body'); clear(body); const title = element('div', 'modal-title', `${module.namespace}/${module.name}/${module.provider}`); title.id = 'modal-title'; body.append(title, element('p', 'artifact-meta', `Terraform module · ${formatNumber(module.versions.length)} versions`));
        const list = element('div', 'version-list'); module.versions.forEach(version => list.appendChild(versionItem(version, () => deleteModule(namespace, name, providerName, version.version), false))); body.appendChild(list); openModal();
    }
    async function deleteProvider(namespace, name, version) { if (!window.confirm(`Delete ${namespace}/${name}@${version}?`)) return; await deleteArtifact(`/providers/${namespace}/${name}/${version}`, loadProviders); }
    async function deleteModule(namespace, name, provider, version) { if (!window.confirm(`Delete ${namespace}/${name}/${provider}@${version}?`)) return; await deleteArtifact(`/modules/${namespace}/${name}/${provider}/${version}`, loadModules); }
    async function deleteArtifact(path, reload) { const key = window.prompt('Write-capable API key:'); if (!key) return; const response = await api(path, { method: 'DELETE', headers: { 'X-API-Key': key } }); toast(response.message || (response.success ? 'Deleted.' : 'Delete failed.'), response.success ? '' : 'error'); closeModal(); reload(); }

    function toggleUploadFields() { const provider = $('upload-type').value === 'provider'; $('module-provider-field').hidden = provider; $('platform-fields').hidden = !provider; }
    async function doUpload() {
        const type = $('upload-type').value; const namespace = $('upload-namespace').value.trim(); const name = $('upload-name').value.trim(); const version = $('upload-version').value.trim(); const file = $('upload-file').files[0]; const key = $('upload-apikey').value.trim(); const result = $('upload-result');
        result.hidden = false; if (!namespace || !name || !version || !file) { result.className = 'result-box error'; result.textContent = 'Namespace, name, version, and artifact file are required.'; return; }
        let path;
        if (type === 'provider') path = `/providers/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}/${encodeURIComponent(version)}/${$('upload-os').value}/${$('upload-arch').value}`;
        else { const provider = $('upload-provider').value.trim(); if (!provider) { result.className = 'result-box error'; result.textContent = 'Module provider is required.'; return; } path = `/modules/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}/${encodeURIComponent(provider)}/${encodeURIComponent(version)}`; }
        const form = new FormData(); form.append('file', file); const headers = {}; if (key) headers['X-API-Key'] = key;
        result.className = 'result-box'; result.textContent = 'Streaming artifact to registry…'; $('upload-button').disabled = true;
        try { const response = await fetch(API_BASE + path, { method: 'POST', headers, body: form }); const data = await response.json(); result.className = `result-box ${data.success ? 'success' : 'error'}`; result.textContent = data.message || (data.success ? 'Published and queued for scanning.' : 'Publish failed.'); if (data.success) loadDashboard(); }
        catch (error) { result.className = 'result-box error'; result.textContent = `Publish failed: ${error.message}`; }
        finally { $('upload-button').disabled = false; }
    }

    function openModal() { $('detail-modal').hidden = false; $('modal-close').focus(); }
    function closeModal() { $('detail-modal').hidden = true; }
    async function copyConfig() { try { await navigator.clipboard.writeText($('registry-config').textContent); $('copy-feedback').textContent = 'Configuration copied.'; window.setTimeout(() => $('copy-feedback').textContent = '', 2200); } catch (_) { toast('Clipboard access unavailable.', 'error'); } }
    function configureAutoRefresh() { window.clearInterval(state.refreshTimer); if ($('security-auto-refresh').checked) state.refreshTimer = window.setInterval(() => { if ($('tab-security').classList.contains('active') && !document.hidden) loadSecurity(); }, 30000); }

    document.querySelectorAll('[data-tab]').forEach(link => link.addEventListener('click', event => { event.preventDefault(); switchTab(link.dataset.tab); }));
    $('provider-search').addEventListener('input', () => renderArtifacts('provider')); $('module-search').addEventListener('input', () => renderArtifacts('module'));
    $('security-search').addEventListener('input', renderSecurity); $('security-kind').addEventListener('change', renderSecurity); $('security-severity').addEventListener('change', renderSecurity);
    document.querySelectorAll('#security-status-group button').forEach(button => button.addEventListener('click', () => { state.securityStatus = button.dataset.value; document.querySelectorAll('#security-status-group button').forEach(item => item.classList.toggle('active', item === button)); renderSecurity(); }));
    $('security-refresh').addEventListener('click', loadSecurity); $('security-auto-refresh').addEventListener('change', configureAutoRefresh); $('upload-type').addEventListener('change', toggleUploadFields); $('upload-button').addEventListener('click', doUpload); $('copy-config').addEventListener('click', copyConfig); $('modal-close').addEventListener('click', closeModal);
    $('detail-modal').addEventListener('click', event => { if (event.target === $('detail-modal')) closeModal(); }); document.addEventListener('keydown', event => { if (event.key === 'Escape') closeModal(); });
    configureAutoRefresh(); switchTab(location.hash.slice(1) || 'dashboard', false);
})();
