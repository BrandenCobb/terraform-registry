// Terraform Registry Command Center
(function () {
    'use strict';

    const API_BASE = '/api/v1';
    const state = { providers: [], modules: [] };
    const $ = id => document.getElementById(id);

    function element(tag, className, text) {
        const node = document.createElement(tag);
        if (className) node.className = className;
        if (text !== undefined && text !== null) node.textContent = String(text);
        return node;
    }
    function clear(node) { node.replaceChildren(); }
    function formatNumber(value) { return Number(value || 0).toLocaleString(); }
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
        if (target === 'upload') toggleUploadFields();
    }

    async function loadDashboard() {
        const stats = await api('/stats');
        if (stats.success) {
            $('stat-providers').textContent = formatNumber(stats.data.providers);
            $('stat-provider-versions').textContent = `${formatNumber(stats.data.provider_versions)} versions`;
            $('stat-modules').textContent = formatNumber(stats.data.modules);
            $('stat-module-versions').textContent = `${formatNumber(stats.data.module_versions)} versions`;
        }
        $('registry-url').textContent = window.location.origin;
        $('system-label').textContent = 'Online';
        $('system-pulse').classList.add('ready');
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
        try { const response = await fetch(API_BASE + path, { method: 'POST', headers, body: form }); const data = await response.json(); result.className = `result-box ${data.success ? 'success' : 'error'}`; result.textContent = data.message || (data.success ? 'Published successfully.' : 'Publish failed.'); if (data.success) loadDashboard(); }
        catch (error) { result.className = 'result-box error'; result.textContent = `Publish failed: ${error.message}`; }
        finally { $('upload-button').disabled = false; }
    }

    function openModal() { $('detail-modal').hidden = false; $('modal-close').focus(); }
    function closeModal() { $('detail-modal').hidden = true; }
    async function copyConfig() { try { await navigator.clipboard.writeText($('registry-config').textContent); $('copy-feedback').textContent = 'Configuration copied.'; window.setTimeout(() => $('copy-feedback').textContent = '', 2200); } catch (_) { toast('Clipboard access unavailable.', 'error'); } }

    document.querySelectorAll('[data-tab]').forEach(link => link.addEventListener('click', event => { event.preventDefault(); switchTab(link.dataset.tab); }));
    $('provider-search').addEventListener('input', () => renderArtifacts('provider')); $('module-search').addEventListener('input', () => renderArtifacts('module'));
    $('upload-type').addEventListener('change', toggleUploadFields); $('upload-button').addEventListener('click', doUpload); $('copy-config').addEventListener('click', copyConfig); $('modal-close').addEventListener('click', closeModal);
    $('detail-modal').addEventListener('click', event => { if (event.target === $('detail-modal')) closeModal(); }); document.addEventListener('keydown', event => { if (event.key === 'Escape') closeModal(); });
    switchTab(location.hash.slice(1) || 'dashboard', false);
})();
