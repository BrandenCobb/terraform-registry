// Terraform Registry Dashboard
(function() {
    'use strict';

    const API_BASE = '/api/v1';

    // Tab navigation
    document.querySelectorAll('.nav-links a').forEach(link => {
        link.addEventListener('click', function(e) {
            e.preventDefault();
            const tab = this.dataset.tab;
            switchTab(tab);
        });
    });

    function switchTab(tab) {
        document.querySelectorAll('.nav-links a').forEach(a => a.classList.remove('active'));
        document.querySelectorAll('.tab-content').forEach(t => t.classList.remove('active'));
        document.querySelector(`[data-tab="${tab}"]`).classList.add('active');
        document.getElementById(`tab-${tab}`).classList.add('active');

        if (tab === 'dashboard') loadStats();
        if (tab === 'providers') loadProviders();
        if (tab === 'modules') loadModules();
        if (tab === 'upload') toggleUploadFields();
    }

    // API helpers
    async function api(path, opts) {
        try {
            const resp = await fetch(API_BASE + path, opts);
            return await resp.json();
        } catch (e) {
            return { success: false, message: e.message };
        }
    }

    // Stats
    async function loadStats() {
        const resp = await api('/stats');
        if (resp.success) {
            document.getElementById('stat-providers').textContent = resp.data.providers;
            document.getElementById('stat-provider-versions').textContent = resp.data.provider_versions;
            document.getElementById('stat-modules').textContent = resp.data.modules;
            document.getElementById('stat-module-versions').textContent = resp.data.module_versions;
        }
        document.getElementById('registry-url').textContent = window.location.origin;
    }

    // Providers
    async function loadProviders() {
        const container = document.getElementById('providers-list');
        container.innerHTML = '<div class="loading">Loading providers...</div>';
        const resp = await api('/providers');
        if (!resp.success || !resp.data || resp.data.length === 0) {
            container.innerHTML = '<div class="empty-state">No providers registered yet.<br>Use the Upload tab or CLI to add providers.</div>';
            return;
        }
        container.innerHTML = resp.data.map(p => `
            <div class="artifact-card" onclick="showProviderDetail('${p.namespace}','${p.name}')">
                <div>
                    <div class="artifact-name">${p.namespace}/${p.name}</div>
                    <div class="artifact-meta">${p.versions ? p.versions.length : 0} version(s)</div>
                </div>
                <div>${(p.versions || []).slice(-3).map(v => `<span class="version-badge">${v.version}</span>`).join('')}</div>
            </div>
        `).join('');
    }

    // Modules
    async function loadModules() {
        const container = document.getElementById('modules-list');
        container.innerHTML = '<div class="loading">Loading modules...</div>';
        const resp = await api('/modules');
        if (!resp.success || !resp.data || resp.data.length === 0) {
            container.innerHTML = '<div class="empty-state">No modules registered yet.<br>Use the Upload tab or CLI to add modules.</div>';
            return;
        }
        container.innerHTML = resp.data.map(m => `
            <div class="artifact-card" onclick="showModuleDetail('${m.namespace}','${m.name}','${m.provider}')">
                <div>
                    <div class="artifact-name">${m.namespace}/${m.name}/${m.provider}</div>
                    <div class="artifact-meta">${m.versions ? m.versions.length : 0} version(s)</div>
                </div>
                <div>${(m.versions || []).slice(-3).map(v => `<span class="version-badge">${v.version}</span>`).join('')}</div>
            </div>
        `).join('');
    }

    // Provider detail modal
    window.showProviderDetail = async function(namespace, name) {
        const resp = await api(`/providers/${namespace}/${name}`);
        if (!resp.success) return;
        const p = resp.data;
        const body = document.getElementById('modal-body');
        body.innerHTML = `
            <div class="modal-title">${p.namespace}/${p.name}</div>
            <p style="color:var(--text-secondary);margin-bottom:1rem">
                Terraform source: <code>${p.namespace}/${p.name}</code>
            </p>
            <h3 style="margin-bottom:0.5rem">Versions (${p.versions.length})</h3>
            <div class="version-list">
                ${p.versions.map(v => `
                    <div class="version-item">
                        <span class="version-badge" style="font-size:0.9rem">${v.version}</span>
                        <div class="platform-list">
                            ${(v.platforms || []).map(pl => `<span class="platform-badge">${pl.os}/${pl.arch}</span>`).join('')}
                        </div>
                        <button class="btn btn-danger" onclick="deleteProvider('${namespace}','${name}','${v.version}')">Delete</button>
                    </div>
                `).join('')}
            </div>
        `;
        document.getElementById('detail-modal').style.display = 'flex';
    };

    // Module detail modal
    window.showModuleDetail = async function(namespace, name, provider) {
        const resp = await api(`/modules/${namespace}/${name}/${provider}`);
        if (!resp.success) return;
        const m = resp.data;
        const body = document.getElementById('modal-body');
        body.innerHTML = `
            <div class="modal-title">${m.namespace}/${m.name}/${m.provider}</div>
            <p style="color:var(--text-secondary);margin-bottom:1rem">
                Terraform source: <code>${m.namespace}/${m.name}/${m.provider}</code>
            </p>
            <h3 style="margin-bottom:0.5rem">Versions (${m.versions.length})</h3>
            <div class="version-list">
                ${m.versions.map(v => `
                    <div class="version-item">
                        <span class="version-badge" style="font-size:0.9rem">${v.version}</span>
                        <button class="btn btn-danger" onclick="deleteModule('${namespace}','${name}','${provider}','${v.version}')">Delete</button>
                    </div>
                `).join('')}
            </div>
        `;
        document.getElementById('detail-modal').style.display = 'flex';
    };

    window.closeModal = function() {
        document.getElementById('detail-modal').style.display = 'none';
    };

    // Delete handlers
    window.deleteProvider = async function(ns, name, version) {
        if (!confirm(`Delete provider ${ns}/${name}@${version}?`)) return;
        const apiKey = prompt('API Key (leave blank if none):');
        const headers = {};
        if (apiKey) headers['X-API-Key'] = apiKey;
        const resp = await fetch(`${API_BASE}/providers/${ns}/${name}/${version}`, { method: 'DELETE', headers });
        const data = await resp.json();
        alert(data.message || 'Done');
        closeModal();
        loadProviders();
    };

    window.deleteModule = async function(ns, name, provider, version) {
        if (!confirm(`Delete module ${ns}/${name}/${provider}@${version}?`)) return;
        const apiKey = prompt('API Key (leave blank if none):');
        const headers = {};
        if (apiKey) headers['X-API-Key'] = apiKey;
        const resp = await fetch(`${API_BASE}/modules/${ns}/${name}/${provider}/${version}`, { method: 'DELETE', headers });
        const data = await resp.json();
        alert(data.message || 'Done');
        closeModal();
        loadModules();
    };

    // Upload
    window.toggleUploadFields = function() {
        const type = document.getElementById('upload-type').value;
        document.getElementById('module-provider-field').style.display = type === 'module' ? 'block' : 'none';
        document.getElementById('platform-fields').style.display = type === 'provider' ? 'flex' : 'none';
    };

    window.doUpload = async function() {
        const type = document.getElementById('upload-type').value;
        const ns = document.getElementById('upload-namespace').value.trim();
        const name = document.getElementById('upload-name').value.trim();
        const version = document.getElementById('upload-version').value.trim();
        const file = document.getElementById('upload-file').files[0];
        const apiKey = document.getElementById('upload-apikey').value.trim();
        const resultEl = document.getElementById('upload-result');

        if (!ns || !name || !version || !file) {
            resultEl.style.display = 'block';
            resultEl.className = 'result-box error';
            resultEl.textContent = 'All fields are required';
            return;
        }

        let url;
        if (type === 'provider') {
            const os = document.getElementById('upload-os').value;
            const arch = document.getElementById('upload-arch').value;
            url = `${API_BASE}/providers/${ns}/${name}/${version}/${os}/${arch}`;
        } else {
            const provider = document.getElementById('upload-provider').value.trim();
            if (!provider) {
                resultEl.style.display = 'block';
                resultEl.className = 'result-box error';
                resultEl.textContent = 'Provider is required for modules';
                return;
            }
            url = `${API_BASE}/modules/${ns}/${name}/${provider}/${version}`;
        }

        const formData = new FormData();
        formData.append('file', file);

        const headers = {};
        if (apiKey) headers['X-API-Key'] = apiKey;

        resultEl.style.display = 'block';
        resultEl.className = 'result-box';
        resultEl.textContent = 'Uploading...';

        try {
            const resp = await fetch(url, { method: 'POST', headers, body: formData });
            const data = await resp.json();
            resultEl.className = `result-box ${data.success ? 'success' : 'error'}`;
            resultEl.textContent = data.message || (data.success ? 'Upload complete' : 'Upload failed');
            if (data.success) {
                loadStats();
            }
        } catch (e) {
            resultEl.className = 'result-box error';
            resultEl.textContent = 'Upload failed: ' + e.message;
        }
    };

    // Initial load
    loadStats();

    // Close modal on outside click
    document.getElementById('detail-modal').addEventListener('click', function(e) {
        if (e.target === this) closeModal();
    });

    // Close modal on Escape
    document.addEventListener('keydown', function(e) {
        if (e.key === 'Escape') closeModal();
    });
})();
