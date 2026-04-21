// --- DRAG UTILITY ---
// Returns a function that reports whether the last mousedown ended as a drag.
function makeDraggable(element, handle) {
    let isDragging = false;
    let didDrag = false;
    let startX = 0;
    let startY = 0;
    let initialLeft = 0;
    let initialTop = 0;

    handle.style.cursor = 'move';

    const onMouseMove = (e) => {
        if (!isDragging) return;
        // Only count as a real drag after the cursor moves more than 5 px.
        if (!didDrag && (Math.abs(e.clientX - startX) > 5 || Math.abs(e.clientY - startY) > 5)) {
            didDrag = true;
        }
        e.preventDefault();
        // Clamp so the element can never be dragged fully off-screen.
        // Keep at least 40 px of the element visible on each edge.
        const MARGIN = 40;
        const rect = element.getBoundingClientRect();
        const vw = window.innerWidth;
        const vh = window.innerHeight;
        const rawLeft = initialLeft + e.clientX - startX;
        const rawTop  = initialTop  + e.clientY - startY;
        const clampedLeft = Math.min(Math.max(rawLeft, MARGIN - rect.width),  vw - MARGIN);
        const clampedTop  = Math.min(Math.max(rawTop,  MARGIN - rect.height), vh - MARGIN);
        element.style.left = `${clampedLeft}px`;
        element.style.top  = `${clampedTop}px`;
        element.style.right = 'auto';
        element.style.bottom = 'auto';
    };

    const onMouseUp = () => {
        isDragging = false;
        if (didDrag) {
            // Stamp a flag on the element so click handlers fired in the same
            // event loop can see that this was a drag, not a tap.
            element.dataset.justDragged = '1';
            requestAnimationFrame(() => requestAnimationFrame(() => {
                delete element.dataset.justDragged;
            }));
        }
        didDrag = false;
        document.removeEventListener('mousemove', onMouseMove);
        document.removeEventListener('mouseup', onMouseUp);
    };

    handle.addEventListener('mousedown', (e) => {
        const targetTag = (e.target && e.target.tagName ? e.target.tagName : '').toLowerCase();
        if (targetTag === 'button' || targetTag === 'input') return;

        isDragging = true;
        didDrag = false;
        startX = e.clientX;
        startY = e.clientY;

        const rect = element.getBoundingClientRect();
        initialLeft = rect.left;
        initialTop = rect.top;

        document.addEventListener('mousemove', onMouseMove);
        document.addEventListener('mouseup', onMouseUp);
    });
}

// Remove any stale launcher from prior script injections.
document.querySelectorAll('button[data-harvester-launcher="true"]').forEach((n) => n.remove());

// Automatically inject a launch button
const launcher = document.createElement('button');
launcher.dataset.harvesterLauncher = 'true';
launcher.textContent = 'Launch Exporter v45';
Object.assign(launcher.style, {
    position: 'fixed',
    bottom: '20px',
    left: '20px',
    zIndex: '999999',
    background: '#9c27b0',
    color: '#fff',
    border: 'none',
    padding: '10px 20px',
    borderRadius: '12px',
    fontWeight: 'bold',
    cursor: 'move',
    boxShadow: '0 4px 10px rgba(0,0,0,0.5)'
});
document.body.appendChild(launcher);
makeDraggable(launcher, launcher);

launcher.addEventListener('click', () => {
    // Suppress click that was the mouseup ending a drag.
    if (launcher.dataset.justDragged) return;
    launcher.style.display = 'none';
    launchHarvesterV45();
});

function launchHarvesterV45() {
    document.querySelectorAll('div[data-harvester-app="true"]').forEach((ui) => ui.remove());
    const staleWizard = document.getElementById('harvester-wizard');
    if (staleWizard) staleWizard.remove();

    const seenFingerprints = new Set();
    const mediaDirectory = [];
    const missingFiles = new Map();
    const executionLogs = [];
    let messageCounter = 0;

    let isScrollingUp = false;
    let isStreaming = false;
    let stopRequested = false;
    let isPaused = false;

    // Anti-throttle and wake lock protections.
    let wakeLock = null;
    let antiThrottleAudio = null;

    async function engageAntiThrottling() {
        try {
            antiThrottleAudio = document.createElement('audio');
            antiThrottleAudio.src = 'data:audio/wav;base64,UklGRiQAAABXQVZFZm10IBAAAAABAAEAQB8AAEAfAAABAAgAZGF0YQAAAAA=';
            antiThrottleAudio.loop = true;
            antiThrottleAudio.volume = 0.01;
            await antiThrottleAudio.play();
            log('Anti-Throttling Media Engine Active.');
        } catch (e) {
            log('Anti-Throttle audio could not start.');
        }

        try {
            if ('wakeLock' in navigator) {
                wakeLock = await navigator.wakeLock.request('screen');
                log('Screen Wake Lock Active.');

                document.addEventListener('visibilitychange', async () => {
                    if (!stopRequested && wakeLock !== null && document.visibilityState === 'visible') {
                        try {
                            wakeLock = await navigator.wakeLock.request('screen');
                            log('Screen Wake Lock Re-engaged.');
                        } catch (e) {
                            log('Wake Lock re-engage failed.');
                        }
                    }
                });
            }
        } catch (e) {
            log(`Wake Lock failed: ${e.message}`);
        }
    }

    async function releaseAntiThrottling() {
        try {
            if (wakeLock) {
                await wakeLock.release();
                wakeLock = null;
                log('Wake Lock Released.');
            }
        } catch (e) {
            log('Wake Lock release failed.');
        }

        if (antiThrottleAudio) {
            antiThrottleAudio.pause();
            antiThrottleAudio.remove();
            antiThrottleAudio = null;
            log('Anti-Throttling Disabled.');
        }
    }

    // --- 1. BUILD MAIN UI ---
    const ui = document.createElement('div');
    ui.dataset.harvesterApp = 'true';
    Object.assign(ui.style, {
        position: 'fixed',
        top: '20px',
        right: '20px',
        zIndex: '999999',
        backgroundColor: '#1e1e1e',
        color: '#fff',
        padding: '15px',
        borderRadius: '8px',
        fontFamily: 'monospace',
        border: '2px solid #00e5ff',
        width: '340px',
        boxShadow: '0 10px 25px rgba(0,0,0,0.8)',
        fontSize: '12px',
        display: 'flex',
        flexDirection: 'column'
    });

    const headerRow = document.createElement('div');
    headerRow.style.display = 'flex';
    headerRow.style.justifyContent = 'space-between';
    headerRow.style.alignItems = 'center';
    headerRow.style.borderBottom = '1px solid #444';
    headerRow.style.paddingBottom = '10px';
    headerRow.style.marginBottom = '10px';

    const title = document.createElement('div');
    title.textContent = 'Master Exporter v45';
    title.style.cssText = 'font-weight:bold; color:#00e5ff; cursor:move; flex-grow:1;';

    const termBtn = document.createElement('button');
    termBtn.textContent = 'Terminate';
    Object.assign(termBtn.style, {
        background: '#f44336',
        color: '#fff',
        border: 'none',
        borderRadius: '4px',
        cursor: 'pointer',
        padding: '4px 8px',
        fontSize: '10px',
        fontWeight: 'bold'
    });

    headerRow.appendChild(title);
    headerRow.appendChild(termBtn);
    ui.appendChild(headerRow);
    makeDraggable(ui, title);

    const autoBoxContainer = document.createElement('div');
    autoBoxContainer.style.marginBottom = '10px';
    autoBoxContainer.innerHTML = '<label style="cursor:pointer; display:flex; align-items:center; gap:5px;"><input type="checkbox" id="auto-phase-2" checked> Auto-start Phase 2 at top</label>';
    ui.appendChild(autoBoxContainer);

    const skipAttachContainer = document.createElement('div');
    skipAttachContainer.style.marginBottom = '10px';
    skipAttachContainer.innerHTML = '<label style="cursor:pointer; display:flex; align-items:center; gap:5px; color:#ccc;"><input type="checkbox" id="skip-attachments"> Skip attachment wizards (mark all missing)</label>';
    ui.appendChild(skipAttachContainer);

    const statsRow = document.createElement('div');
    statsRow.style.cssText = 'display:flex; justify-content:space-between; align-items:center; margin-bottom:5px;';
    const stats = document.createElement('span');
    stats.textContent = 'Messages: 0 | Media: 0';
    const minimizeLogBtn = document.createElement('button');
    minimizeLogBtn.textContent = '\u2212';
    minimizeLogBtn.title = 'Toggle log panel';
    Object.assign(minimizeLogBtn.style, {
        background: 'transparent', color: '#888', border: '1px solid #444',
        borderRadius: '3px', cursor: 'pointer', fontSize: '11px', padding: '0 5px', lineHeight: '16px'
    });
    minimizeLogBtn.onclick = () => {
        const isHidden = logBox.style.display === 'none';
        logBox.style.display = isHidden ? '' : 'none';
        minimizeLogBtn.textContent = isHidden ? '\u2212' : '+';
    };
    statsRow.appendChild(stats);
    statsRow.appendChild(minimizeLogBtn);
    ui.appendChild(statsRow);

    const logBox = document.createElement('div');
    logBox.style.cssText = 'font-size:10px; color:#0f0; margin-bottom:15px; padding:8px; background:#000; border-radius:4px; height:120px; overflow-y:auto; border:1px solid #333;';
    logBox.textContent = 'Extension Loaded. Choose Phase 1 or 2.';
    ui.appendChild(logBox);

    const upBtn = document.createElement('button');
    upBtn.textContent = 'Phase 1: Smart Load (Up)';
    Object.assign(upBtn.style, {
        width: '100%',
        padding: '10px',
        background: '#4285F4',
        border: 'none',
        color: '#fff',
        borderRadius: '4px',
        cursor: 'pointer',
        fontWeight: 'bold',
        marginBottom: '10px'
    });
    ui.appendChild(upBtn);

    const btnContainer = document.createElement('div');
    btnContainer.style.display = 'flex';
    btnContainer.style.gap = '5px';

    const downBtn = document.createElement('button');
    downBtn.textContent = 'Phase 2: Save';
    Object.assign(downBtn.style, {
        flex: '2',
        padding: '10px',
        background: '#9c27b0',
        border: 'none',
        color: '#fff',
        borderRadius: '4px',
        cursor: 'pointer',
        fontWeight: 'bold'
    });

    const pauseBtn = document.createElement('button');
    pauseBtn.textContent = 'Pause';
    Object.assign(pauseBtn.style, {
        flex: '1',
        padding: '10px',
        background: '#ff9800',
        border: 'none',
        color: '#fff',
        borderRadius: '4px',
        cursor: 'pointer',
        fontWeight: 'bold',
        display: 'none'
    });

    btnContainer.appendChild(downBtn);
    btnContainer.appendChild(pauseBtn);
    ui.appendChild(btnContainer);
    document.body.appendChild(ui);

    // --- TERMINATE LOGIC ---
    termBtn.onclick = async () => {
        stopRequested = true;
        isPaused = false;
        isScrollingUp = false;
        exportDiagnosticLogs('terminated');

        ui.remove();
        const wizard = document.getElementById('harvester-wizard');
        if (wizard) wizard.remove();

        launcher.style.display = 'block';
        document.querySelectorAll('[style*="outline: 4px solid"]').forEach((el) => {
            el.style.outline = '';
        });

        await releaseAntiThrottling();
        document.removeEventListener('keydown', keyboardHandler);
        console.log('Harvester Terminated.');
    };

    // --- PAUSE LOGIC ---
    pauseBtn.onclick = () => {
        isPaused = !isPaused;
        pauseBtn.textContent = isPaused ? 'Resume' : 'Pause';
        pauseBtn.style.background = isPaused ? '#4caf50' : '#ff9800';
        log(isPaused ? 'Script Paused.' : 'Script Resumed.');
    };

    // --- KEYBOARD SHORTCUT: Space = Pause / Resume ---
    const keyboardHandler = (e) => {
        const tag = document.activeElement ? document.activeElement.tagName.toLowerCase() : '';
        if (tag === 'input' || tag === 'textarea' || tag === 'select') return;
        if (e.code === 'Space' && (isScrollingUp || isStreaming) && !stopRequested) {
            e.preventDefault();
            pauseBtn.click();
        }
    };
    document.addEventListener('keydown', keyboardHandler);

    // --- WIZARD UI (Non-blocking, Draggable) ---
    const wizardBox = document.createElement('div');
    wizardBox.id = 'harvester-wizard';
    wizardBox.dataset.harvesterApp = 'true';
    Object.assign(wizardBox.style, {
        position: 'fixed',
        top: '20%',
        left: '50%',
        transform: 'translateX(-50%)',
        background: '#1e1e1e',
        padding: '20px',
        borderRadius: '12px',
        width: '480px',
        textAlign: 'center',
        color: '#fff',
        fontFamily: 'sans-serif',
        border: '2px solid #ff9800',
        boxShadow: '0 10px 40px rgba(0,0,0,0.8)',
        zIndex: '9999999',
        display: 'none',
        flexDirection: 'column'
    });

    const wizHeader = document.createElement('div');
    // Empty drag-handle bar at top of wizard — no label needed.
    wizHeader.innerHTML = '&#8942;&nbsp;&#8942;&nbsp;&#8942;';
    Object.assign(wizHeader.style, {
        background: '#2a2a2a',
        margin: '-20px -20px 15px -20px',
        padding: '6px',
        borderRadius: '10px 10px 0 0',
        cursor: 'move',
        fontSize: '14px',
        color: '#555',
        textAlign: 'center',
        letterSpacing: '4px'
    });
    wizardBox.appendChild(wizHeader);

    const wizContent = document.createElement('div');
    wizardBox.appendChild(wizContent);
    document.body.appendChild(wizardBox);
    makeDraggable(wizardBox, wizHeader);

    wizardBox.addEventListener('keydown', (e) => {
        if (e.key !== 'Tab') return;
        const focusable = Array.from(wizardBox.querySelectorAll('button:not([style*="display: none"])'));
        if (!focusable.length) return;

        const first = focusable[0];
        const last = focusable[focusable.length - 1];
        if (e.shiftKey && document.activeElement === first) {
            e.preventDefault();
            last.focus();
        } else if (!e.shiftKey && document.activeElement === last) {
            e.preventDefault();
            first.focus();
        }
    });

    // --- LOGGING ENGINE ---
    function log(msg) {
        const timestamp = new Date().toISOString().split('T')[1].slice(0, -1);

        if (executionLogs.length > 5000) executionLogs.shift();
        executionLogs.push(`[${timestamp}] ${msg}`);

        const p = document.createElement('div');
        p.textContent = `> ${msg}`;
        logBox.appendChild(p);
        logBox.scrollTop = logBox.scrollHeight;

        if (logBox.children.length > 80) {
            logBox.removeChild(logBox.firstChild);
        }
    }

    function exportDiagnosticLogs(reason = 'run') {
        if (executionLogs.length === 0) return;
        const stamp = new Date().toISOString().replace(/[:.]/g, '-');
        const fileName = `Harvester_Diagnostics_Log_v45_${reason}_${stamp}.txt`;
        const blob = new Blob([executionLogs.join('\n')], { type: 'text/plain' });
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = fileName;
        document.body.appendChild(a);
        a.click();
        document.body.removeChild(a);
        URL.revokeObjectURL(url);
        log(`Diagnostic logs exported: ${fileName}`);
    }

    function getScroller() {
        const msgs = document.querySelectorAll('user-query, model-response, [data-message-author]');
        if (msgs.length > 0) {
            let parent = msgs[0].parentElement;
            while (parent && parent !== document.body) {
                const style = window.getComputedStyle(parent);
                if ((style.overflowY === 'auto' || style.overflowY === 'scroll') && parent.scrollHeight > parent.clientHeight) {
                    return parent;
                }
                parent = parent.parentElement;
            }
        }
        return window;
    }

    // Lookup table hoisted outside the regex callback — avoids creating a new
    // object literal on every matched character.
    const HTML_ESCAPE_MAP = { '&': '&amp;', '<': '&lt;', '>': '&gt;', "'": '&#39;', '"': '&quot;' };
    function escapeHTML(str) {
        return str.replace(/[&<>'"]/g, (tag) => HTML_ESCAPE_MAP[tag] || tag);
    }

    // Reject javascript:, data:, and other non-http(s) URIs before placing
    // them in href attributes to prevent XSS via crafted filenames.
    function safeUrl(url) {
        if (!url) return null;
        try {
            const parsed = new URL(url, location.href);
            if (parsed.protocol !== 'https:' && parsed.protocol !== 'http:') return null;
        } catch (e) {
            return null;
        }
        return url;
    }

    function getFullFileName(previewEl) {
        let text = previewEl.innerText || previewEl.textContent || '';
        text = text.replace(/Remove file/gi, '').replace(/\n/g, '').trim();

        const match = text.match(/([a-zA-Z0-9_\-\s\(\)]+\.[a-zA-Z0-9]{2,5})/);
        if (match) return match[1].trim();

        const attr = previewEl.getAttribute('aria-label') || previewEl.getAttribute('mattooltip') || previewEl.title;
        if (attr) return attr.replace(/Remove file/gi, '').replace(/Attachment:/gi, '').trim();

        return text.substring(0, 30) || 'Unknown_Attachment';
    }

    async function toBase64(url) {
        return new Promise((resolve) => {
            if (!chrome.runtime || !chrome.runtime.sendMessage) {
                resolve(url);
                return;
            }
            chrome.runtime.sendMessage({ action: 'fetchBase64', url }, (response) => {
                if (response && response.base64) resolve(response.base64);
                else resolve(url);
            });
        });
    }

    async function getBase64Image(imgElement) {
        if (!imgElement) return null;

        const src = imgElement.src;
        if (!src) return null;
        if (src.startsWith('data:')) return src;

        if (src.startsWith('blob:')) {
            return new Promise((resolve) => {
                const canvas = document.createElement('canvas');
                canvas.width = imgElement.naturalWidth || imgElement.width || 800;
                canvas.height = imgElement.naturalHeight || imgElement.height || 600;
                const ctx = canvas.getContext('2d');
                if (!ctx) {
                    resolve(null);
                    return;
                }
                ctx.drawImage(imgElement, 0, 0);
                try {
                    resolve(canvas.toDataURL('image/png'));
                } catch (e) {
                    resolve(null);
                }
            });
        }

        return toBase64(src);
    }

    // MutationObserver-based wait: resolves the instant the element appears
    // instead of polling every 150ms (saves up to 150ms per call on average).
    function waitForEl(selector, timeout = 4000) {
        return new Promise((resolve) => {
            if (stopRequested) { resolve(null); return; }
            const existing = document.querySelector(selector);
            if (existing) { resolve(existing); return; }
            let settled = false;
            const settle = (el) => {
                if (settled) return;
                settled = true;
                clearTimeout(timer);
                observer.disconnect();
                resolve(el);
            };
            const timer = setTimeout(() => settle(null), timeout);
            const observer = new MutationObserver(() => {
                if (stopRequested) { settle(null); return; }
                const el = document.querySelector(selector);
                if (el) settle(el);
            });
            observer.observe(document.body, {
                childList: true,
                subtree: true,
                attributes: true,
                attributeFilter: ['open', 'class']
            });
        });
    }

    // --- 5-BUTTON UNIVERSAL WIZARD LOGIC (Non-blocking) ---
    function openWizard(label, previewHtml, link, mode, reason = '') {
        return new Promise((resolve) => {
            wizardBox.style.display = 'flex';
            log(`Wizard Opened. Target: ${label}`);

            const renderDropZone = () => {
                wizContent.innerHTML = `
                    <h2 style="color:#9c27b0; margin-top:0;">Drag and Drop File</h2>
                    <p style="color:#ccc;">Please drag <strong>${escapeHTML(label)}</strong> from your computer into the box below.</p>
                    <div id="drop-zone" style="border:3px dashed #9c27b0; padding:40px; border-radius:10px; color:#dcdcdc; background:#2a2a2a; margin-top:10px; transition:0.3s;">
                        Drop <strong>${escapeHTML(label)}</strong> here
                    </div>
                    <button id="wiz-cancel-drop" style="margin-top:15px; padding:8px 15px; background:#444; border:none; color:#fff; border-radius:5px; cursor:pointer;">Cancel and Mark Missing</button>
                `;

                setTimeout(() => {
                    const cancelBtn = document.getElementById('wiz-cancel-drop');
                    if (cancelBtn) cancelBtn.focus();
                }, 50);

                const dropZone = document.getElementById('drop-zone');
                if (!dropZone) return;

                dropZone.addEventListener('dragover', (e) => {
                    e.preventDefault();
                    dropZone.style.background = '#4a148c';
                });
                dropZone.addEventListener('dragleave', () => {
                    dropZone.style.background = '#2a2a2a';
                });

                dropZone.addEventListener('drop', (e) => {
                    e.preventDefault();
                    dropZone.style.background = '#34A853';
                    dropZone.textContent = 'Processing... Please wait.';

                    if (!e.dataTransfer || !e.dataTransfer.files || e.dataTransfer.files.length < 1) return;
                    const file = e.dataTransfer.files[0];
                    const isText = file.type.startsWith('text/') || file.name.match(/\.(json|py|js|csv|html|css|cpp|c|md|log|xml|sh)$/i);

                    if (isText) {
                        const reader = new FileReader();
                        reader.onload = (event) => {
                            wizardBox.style.display = 'none';
                            resolve({ action: 'dropped_text', text: event.target.result, name: file.name, link });
                        };
                        reader.readAsText(file);
                    } else {
                        const reader = new FileReader();
                        reader.onload = (event) => {
                            wizardBox.style.display = 'none';
                            resolve({ action: 'dropped_media', data: event.target.result, name: file.name, type: file.type, link });
                        };
                        reader.readAsDataURL(file);
                    }
                });

                const cancelBtn = document.getElementById('wiz-cancel-drop');
                if (cancelBtn) {
                    cancelBtn.onclick = () => {
                        wizardBox.style.display = 'none';
                        resolve({ action: 'skip', link });
                    };
                }
            };

            if (mode === 'dropzone_only') {
                renderDropZone();
                return;
            }

            const safeLink = safeUrl(link);
            const linkHtml = safeLink
                ? `<a href="${safeLink}" target="_blank" style="color:#00e5ff; font-weight:bold; display:block; margin-bottom:15px;">Download Original from Google Drive</a>`
                : '';
            const headerHtml = mode === 'initial'
                ? '<h2 style="color:#ff9800; margin-top:0;">Attachment Detected</h2>'
                : `<h2 style="color:#f44336; margin-top:0;">Processing Failed</h2><p style="color:#ffb74d;">${escapeHTML(reason)}</p>`;

            wizContent.innerHTML = `
                ${headerHtml}
                <p style="font-weight:bold; color:#fff; word-break:break-all;">File: ${escapeHTML(label)}</p>
                <div style="background:#000; padding:10px; border-radius:8px; margin:15px 0; display:flex; justify-content:center; align-items:center; max-height:100px; overflow:hidden;">
                    ${previewHtml}
                </div>
                <p style="color:#ccc; font-size:11px;">(You can interact with the page behind this window if needed)</p>
                ${linkHtml}
                <div style="display:grid; grid-template-columns:1fr 1fr; gap:10px; margin-top:20px;">
                    <button id="wiz-text" style="padding:12px; background:#34A853; border:none; color:#fff; border-radius:5px; cursor:pointer; font-weight:bold;">Auto-Read Text</button>
                    <button id="wiz-img" style="padding:12px; background:#2196f3; border:none; color:#fff; border-radius:5px; cursor:pointer; font-weight:bold;">Extract Image</button>
                    <button id="wiz-vid" style="padding:12px; background:#ff9800; border:none; color:#fff; border-radius:5px; cursor:pointer; font-weight:bold;">Extract Video</button>
                    <button id="wiz-import" style="padding:12px; background:#9c27b0; border:none; color:#fff; border-radius:5px; cursor:pointer; font-weight:bold;">Import File</button>
                    <button id="wiz-skip" style="grid-column:span 2; padding:12px; background:#757575; border:none; color:#fff; border-radius:5px; cursor:pointer; font-weight:bold;">Mark Missing</button>
                </div>
            `;

            setTimeout(() => {
                const focusBtn = document.getElementById('wiz-text');
                if (focusBtn) focusBtn.focus();
            }, 50);

            document.getElementById('wiz-text').onclick = () => {
                wizardBox.style.display = 'none';
                resolve({ action: 'auto_read', link });
            };
            document.getElementById('wiz-img').onclick = () => {
                wizardBox.style.display = 'none';
                resolve({ action: 'extract_image', link });
            };
            document.getElementById('wiz-vid').onclick = () => {
                wizardBox.style.display = 'none';
                resolve({ action: 'extract_video', link });
            };
            document.getElementById('wiz-import').onclick = () => {
                renderDropZone();
            };
            document.getElementById('wiz-skip').onclick = () => {
                wizardBox.style.display = 'none';
                resolve({ action: 'skip', link });
            };
        });
    }

    async function closeActiveViewers() {
        let waitClose = 0;
        const containerSelectors = '.file-preview-sidebar, mat-sidenav, .mat-drawer, .mat-drawer-opened, dialog[open], [role="dialog"], .fullscreen-preview';

        while (document.querySelector(containerSelectors) && waitClose < 15 && !stopRequested) {
            const activeOverlays = document.querySelectorAll(containerSelectors);
            activeOverlays.forEach((overlay) => {
                const firstButton = overlay.querySelector('button');
                if (firstButton) {
                    try {
                        firstButton.click();
                    } catch (e) {
                        // no-op
                    }
                }

                overlay.querySelectorAll('button').forEach((btn) => {
                    const label = (btn.getAttribute('aria-label') || '').toLowerCase();
                    const tooltip = (btn.getAttribute('mattooltip') || '').toLowerCase();
                    const inner = btn.innerHTML.toLowerCase();
                    if (label.includes('close') || label.includes('hide') || tooltip.includes('close') || inner.includes('close')) {
                        try {
                            btn.click();
                        } catch (e) {
                            // no-op
                        }
                    }
                });
            });

            document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
            await new Promise((r) => setTimeout(r, 100));
            waitClose += 1;
        }
    }

    // --- PHASE 1: SCROLL UP ---
    upBtn.onclick = async () => {
        if (stopRequested) return;
        isScrollingUp = true;
        upBtn.style.display = 'none';
        pauseBtn.style.display = 'block';
        log('Phase 1: Upward Scroll Engaged');
        await engageAntiThrottling();

        const scroller = getScroller();
        let topCount = 0;

        while (isScrollingUp && !stopRequested) {
            while (isPaused && !stopRequested) {
                await new Promise((r) => setTimeout(r, 200));
            }
            if (stopRequested) break;

            const previousHeight = scroller.scrollHeight || document.documentElement.scrollHeight;
            if (scroller !== window) scroller.scrollTop = 0;
            else window.scrollTo(0, 0);

            let waitTime = 0;
            let currentHeight = previousHeight;
            while (currentHeight === previousHeight && waitTime < 6000 && isScrollingUp && !isPaused && !stopRequested) {
                await new Promise((r) => setTimeout(r, 200));
                waitTime += 200;
                currentHeight = scroller.scrollHeight || document.documentElement.scrollHeight;
            }

            if (currentHeight > previousHeight) {
                topCount = 0;
                log('Loaded chunk. Continuing...');
            } else {
                topCount += 1;
                log(`Waiting for new data... (${topCount}/5)`);

                if (topCount >= 5) {
                    isScrollingUp = false;
                    log('Top reached.');
                    const autoCheckbox = document.getElementById('auto-phase-2');
                    if (autoCheckbox && autoCheckbox.checked) {
                        downBtn.style.boxShadow = '0 0 0 2px #00e5ff inset';
                        log('Phase 2 is ready. Click "Phase 2: Save" to open the save dialog.');
                    }
                }
            }
        }

        if (!isStreaming) {
            await releaseAntiThrottling();
        }
    };

    // --- PHASE 2: DOWNWARD CRAWL & DEEP FETCH ---
    downBtn.onclick = async () => {
        isScrollingUp = false;
        if (isStreaming || stopRequested) return;
        downBtn.style.boxShadow = '';

        let chatTitle = document.title.split('-')[0].trim().replace(/[^a-zA-Z0-9 \-_]/g, '_');
        if (!chatTitle || chatTitle === 'Gemini') chatTitle = 'Gemini_Archive';
        const exportDate = new Date().toISOString().slice(0, 10);
        const defaultFileName = `${chatTitle}_${exportDate}.html`;

        let fileHandle;
        let writable;
        try {
            fileHandle = await window.showSaveFilePicker({
                suggestedName: defaultFileName,
                types: [{ description: 'HTML Document', accept: { 'text/html': ['.html'] } }]
            });
            writable = await fileHandle.createWritable();
        } catch (e) {
            if (e && e.name === 'AbortError') {
                log('Save dialog was closed before selecting a file.');
                exportDiagnosticLogs('save-dialog-closed');
            } else if (e && (e.name === 'NotAllowedError' || e.name === 'SecurityError')) {
                log('Save dialog blocked by browser security. Click "Phase 2: Save" directly to continue.');
                exportDiagnosticLogs('save-dialog-blocked');
            } else {
                log(`Save dialog failed: ${e && e.message ? e.message : 'unknown error'}`);
                exportDiagnosticLogs('save-dialog-error');
            }
            return;
        }

        await engageAntiThrottling();

        isStreaming = true;
        downBtn.disabled = true;
        upBtn.style.display = 'none';
        pauseBtn.style.display = 'block';
        const autoLabel = document.getElementById('auto-phase-2');
        if (autoLabel && autoLabel.parentElement) {
            autoLabel.parentElement.style.display = 'none';
        }
        const skipElOuter = document.getElementById('skip-attachments');
        if (skipElOuter && skipElOuter.parentElement && skipElOuter.parentElement.parentElement) {
            skipElOuter.parentElement.parentElement.style.display = 'none';
        }
        log(`File Stream Opened: ${defaultFileName}`);

        await writable.write(`<html><head><meta charset="UTF-8"><style>
            body { font-family: -apple-system, sans-serif; max-width: 850px; margin: 40px auto; padding: 20px; background: #f4f7f6; line-height: 1.6; }
            .entry { margin-bottom: 25px; padding: 20px; border-radius: 12px; background: white; border: 1px solid #e0e0e0; word-wrap: break-word; }
            .USER-QUERY, [data-message-author="user"] { border-left: 6px solid #9c27b0; }
            .MODEL-RESPONSE, [data-message-author="model"] { border-left: 6px solid #00c853; }
            .map-box { background: #fffde7; border: 2px solid #fbc02d; padding: 20px; border-radius: 10px; margin-top: 40px; }
            pre { background: #1e1e1e; color: #dcdcdc; padding: 15px; border-radius: 8px; overflow-x: auto; }
            .media-marker { background:#f3e5f5; color:#7b1fa2; border:2px dashed #9c27b0; padding:12px; margin:15px 0; text-align:center; font-weight:bold; border-radius: 6px; }
            .deep-fetch-container { display: flex; flex-direction: column; background: #1e1e1e; border: 1px solid #444; border-radius: 8px; margin-top: 15px; max-height: 600px; overflow: hidden; box-shadow: 0 4px 6px rgba(0,0,0,0.3); }
            .deep-fetch-header { background: #2d2d2d; padding: 10px 15px; border-bottom: 1px solid #444; color: #00e5ff; font-weight: bold; position: sticky; top: 0; z-index: 5; }
            .deep-fetch-content { padding: 15px; overflow-y: auto; color: #dcdcdc; }
            .deep-fetch-content pre { background: transparent; padding: 0; margin: 0; font-family: Consolas, monospace; white-space: pre-wrap; word-break: break-word; }
            img { max-width: 100%; height: auto; border-radius: 8px; margin-top: 10px; }
            video { max-width: 100%; border-radius: 8px; margin-top: 10px; }
            a.jump { background: #9c27b0; color: white; text-decoration: none; padding: 4px 10px; border-radius: 5px; font-weight: bold; float: right; font-size: 12px; }
            a.download-link { display: inline-block; padding: 10px 15px; background: #2196f3; color: #fff; text-decoration: none; border-radius: 5px; font-weight: bold; margin-top: 10px; }
            model-thoughts, .thoughts-container { display: block; background: #e8f5e9; padding: 10px; margin-bottom: 15px; border-radius: 6px; font-size: 0.9em; border-left: 4px solid #4caf50; }
        </style></head><body>
        <a href="#media-appendix" style="display:block; text-align:center; padding:15px; background:#f29900; color:#000; font-weight:bold; text-decoration:none; margin-bottom:20px; border-radius:8px;">JUMP TO MEDIA APPENDIX</a>
        <h1>${escapeHTML(chatTitle)}</h1>\n\n`);

        const exportStartTime = Date.now();
        const preflightCount = document.querySelectorAll('user-query, model-response, [data-message-author]').length;
        log(`Pre-flight: ~${preflightCount} message blocks detected.`);
        stats.textContent = `0 / ~${preflightCount} | 0 media`;
        let stuckCounter = 0;
        const scroller = getScroller();
        const filePreviewSelector = 'user-query-file-preview, file-preview, mat-chip, .attachment-chip, [data-test-id="file-preview"], [data-test-id="uploaded-file"]';
        // Pre-joined selector string — avoids array alloc + join on every message block.
        const CLUTTER_SEL = 'img[src*="avatar"], .model-s-icon, .avatar, .action-row, .response-footer-container, .feedback-container, .response-container-header, .avatar-gutter, tts-control, .response-tts-container, button';
        // Cache the checkbox element — avoid getElementById inside the hot attachment loop.
        const skipAllAttach = document.getElementById('skip-attachments');

        while (!stopRequested && stuckCounter < 12) {
            while (isPaused && !stopRequested) {
                document.querySelectorAll('[style*="outline: 4px solid"]').forEach((el) => {
                    el.style.outline = '';
                });
                await new Promise((r) => setTimeout(r, 200));
            }
            if (stopRequested) break;

            const blocks = Array.from(document.querySelectorAll('user-query, model-response, [data-message-author]'))
                .filter((node) => !node.dataset.v45Captured && !node.parentElement.closest('user-query, model-response, [data-message-author]'));
            let newMessagesThisLoop = 0;

            for (const el of blocks) {
                if (el.dataset.v45Captured) continue;
                if (stopRequested) break;
                while (isPaused && !stopRequested) {
                    await new Promise((r) => setTimeout(r, 200));
                }

                el.scrollIntoView({ behavior: 'smooth', block: 'center' });
                el.style.outline = '4px solid #9c27b0';
                await new Promise((r) => setTimeout(r, 300));

                const contentAreas = el.querySelectorAll('.message-content, .prompt-text, .response-text');
                contentAreas.forEach((area) => {
                    area.querySelectorAll('button').forEach((b) => {
                        const btnLabel = (b.getAttribute('aria-label') || '').toLowerCase();
                        const tooltip = (b.getAttribute('mattooltip') || '').toLowerCase();
                        const hasPopup = b.getAttribute('aria-haspopup');

                        if (hasPopup === 'menu' || hasPopup === 'true' || hasPopup === 'dialog') return;
                        if (btnLabel.includes('tool') || tooltip.includes('tool')) return;
                        if (btnLabel.includes('share') || btnLabel.includes('export') || btnLabel.includes('more action')) return;

                        if ((btnLabel.includes('expand') || btnLabel.includes('show more')) && !b.dataset.harvesterExpanded) {
                            b.dataset.harvesterExpanded = 'true';
                            try {
                                b.click();
                            } catch (e) {
                                // no-op
                            }
                        }
                    });
                });
                await new Promise((r) => setTimeout(r, 150));

                const targetContent = el.querySelector('.message-content') || el;
                const rawText = targetContent.textContent.trim();

                const allFilePreviews = Array.from(targetContent.querySelectorAll(filePreviewSelector));
                const filePreviews = allFilePreviews.filter((p) => !allFilePreviews.some((parent) => parent !== p && parent.contains(p)));

                // Compute once per block — cached array of live DOM refs, no re-querying inside the loop.
                const validImages = Array.from(targetContent.querySelectorAll('img:not(.avatar):not([src*="avatar"])'))
                    .filter((img) => !img.closest(filePreviewSelector) && img.naturalWidth > 20 && !img.src.includes('icon'));

                if (rawText.length < 1 && validImages.length === 0 && filePreviews.length === 0) continue;

                const fingerprint = `${rawText.substring(0, 50).replace(/\s/g, '')}_${targetContent.innerHTML.length}`;
                if (seenFingerprints.has(fingerprint)) {
                    el.dataset.v45Captured = 'true';
                    continue;
                }

                messageCounter += 1;
                newMessagesThisLoop += 1;
                const blockId = `msg-${messageCounter}`;

                const mediaFound = [];
                const processedAttachments = new Set();
                let safeMediaInjectionHTML = '';

                log(`Processing Block ${messageCounter}`);

                const imgCount = validImages.length;
                for (let i = 0; i < imgCount; i += 1) {
                    if (stopRequested) break;
                    while (isPaused && !stopRequested) await new Promise((r) => setTimeout(r, 200));

                    log(`Attempting Pure Image Fetch ${i + 1}/${imgCount}...`);
                    try {
                        const liveImg = validImages[i];
                        if (!liveImg || !document.contains(liveImg)) {
                            log('Image element lost from DOM. Skipping.');
                            continue;
                        }

                        const clickTarget = liveImg.closest('button, [role="button"], .preview-image-button, a') || liveImg;
                        clickTarget.click();

                        const modalImg = await waitForEl('dialog img:not(.avatar), .fullscreen-preview img:not(.avatar)', 4000);
                        // img.decode() resolves the instant the image is painted — no fixed 500ms sleep.
                        if (modalImg) {
                            try { await Promise.race([modalImg.decode(), new Promise((r) => setTimeout(r, 600))]); } catch (e) { /* no-op */ }
                        }

                        let b64 = await getBase64Image(modalImg || liveImg);
                        if (b64) {
                            safeMediaInjectionHTML += `<div class="deep-fetch-container"><div class="deep-fetch-header">Image Attachment ${i + 1}</div><div class="deep-fetch-content"><img src="${b64}"/></div></div>`;
                            mediaFound.push('IMAGE');
                        }
                        b64 = null;
                        await closeActiveViewers();
                    } catch (e) {
                        log(`Image Extraction failed: ${e.message}`);
                        await closeActiveViewers();
                    }
                }

                for (const preview of filePreviews) {
                    if (stopRequested) break;
                    while (isPaused && !stopRequested) await new Promise((r) => setTimeout(r, 200));

                    let label = getFullFileName(preview);
                    const originalLabel = label;
                    let duplicateCounter = 2;
                    while (processedAttachments.has(label)) {
                        label = `${originalLabel} (${duplicateCounter})`;
                        duplicateCounter += 1;
                    }
                    processedAttachments.add(label);

                    const aTag = preview.querySelector('a');
                    const linkHref = safeUrl(preview.getAttribute('href') || (aTag ? aTag.getAttribute('href') : null));
                    const safePreviewHtml = preview.outerHTML.replace(/<svg.*?<\/svg>/g, '');

                    log(`Evaluating Attachment: ${label}`);
                    if (skipAllAttach && skipAllAttach.checked) {
                        missingFiles.set(label, { label, link: linkHref });
                        mediaFound.push(`SKIPPED: ${label}`);
                        safeMediaInjectionHTML += `<div class="media-marker">[ SKIPPED: ${escapeHTML(label)} ]</div>`;
                        continue;
                    }
                    let needsWizard = true;
                    let mode = 'initial';
                    let reason = '';

                    while (needsWizard && !stopRequested) {
                        const decision = await openWizard(label, safePreviewHtml, linkHref, mode, reason);
                        const wizardChoice = decision.action;
                        needsWizard = false;

                        if (wizardChoice === 'skip') {
                            missingFiles.set(label, { label, link: linkHref });
                            mediaFound.push(`MISSING: ${label}`);
                            safeMediaInjectionHTML += `<div class="media-marker" style="background:#ffebee; border-color:#f44336; color:#d32f2f;">[ MISSING FILE: ${label} ] ${linkHref ? `<br><a href="${linkHref}" target="_blank">Drive Link</a>` : ''}</div>`;
                        } else if (wizardChoice === 'dropped_text') {
                            safeMediaInjectionHTML += `<div class="deep-fetch-container"><div class="deep-fetch-header">${decision.name}</div><div class="deep-fetch-content"><pre>${escapeHTML(decision.text)}</pre></div></div>`;
                            mediaFound.push(`IMPORTED: ${decision.name}`);
                        } else if (wizardChoice === 'dropped_media') {
                            let embedHtml = '';
                            if (decision.type.includes('video')) embedHtml = `<video controls src="${decision.data}"></video>`;
                            else if (decision.type.includes('image')) embedHtml = `<img src="${decision.data}"/>`;
                            else embedHtml = `<a class="download-link" href="${decision.data}" download="${decision.name}">Download ${decision.name}</a>`;

                            safeMediaInjectionHTML += `<div class="deep-fetch-container"><div class="deep-fetch-header">${decision.name}</div><div class="deep-fetch-content">${embedHtml}</div></div>`;
                            mediaFound.push(`IMPORTED: ${decision.name}`);
                            decision.data = null;
                        } else if (wizardChoice === 'extract_image') {
                            try {
                                const clickTarget = preview.querySelector('button, a') || preview;
                                clickTarget.click();
                                const modalImg = await waitForEl('dialog img:not(.avatar), .fullscreen-preview img:not(.avatar)', 4000);
                                if (modalImg) {
                                    try { await Promise.race([modalImg.decode(), new Promise((r) => setTimeout(r, 600))]); } catch (e) { /* no-op */ }
                                }
                                let b64 = await getBase64Image(modalImg || preview.querySelector('img'));
                                if (b64 && b64 !== '') {
                                    safeMediaInjectionHTML += `<div class="deep-fetch-container"><div class="deep-fetch-header">${label}</div><div class="deep-fetch-content"><img src="${b64}"/></div></div>`;
                                    mediaFound.push(`FETCHED IMAGE: ${label}`);
                                } else {
                                    throw new Error('Image blank');
                                }
                                b64 = null;
                                await closeActiveViewers();
                            } catch (e) {
                                await closeActiveViewers();
                                mode = 'fallback';
                                reason = 'Failed to load high-res image.';
                                needsWizard = true;
                            }
                        } else if (wizardChoice === 'extract_video') {
                            try {
                                const clickTarget = preview.querySelector('button, a') || preview;
                                clickTarget.click();

                                const readyBtn = document.createElement('button');
                                readyBtn.textContent = 'I downloaded the video. Ready to Import';
                                Object.assign(readyBtn.style, {
                                    position: 'fixed',
                                    bottom: '30px',
                                    left: '30px',
                                    zIndex: '9999999',
                                    background: '#00c853',
                                    color: '#fff',
                                    border: 'none',
                                    padding: '15px 30px',
                                    borderRadius: '8px',
                                    fontWeight: 'bold',
                                    cursor: 'pointer',
                                    fontSize: '16px',
                                    boxShadow: '0 4px 10px rgba(0,0,0,0.5)'
                                });
                                document.body.appendChild(readyBtn);
                                await new Promise((resolveClick) => {
                                    readyBtn.onclick = () => {
                                        readyBtn.remove();
                                        resolveClick();
                                    };
                                });
                                await closeActiveViewers();

                                const dropDecision = await openWizard(label, safePreviewHtml, linkHref, 'dropzone_only');
                                if (dropDecision.action === 'skip') {
                                    missingFiles.set(label, { label, link: linkHref });
                                    mediaFound.push(`MISSING: ${label}`);
                                    safeMediaInjectionHTML += `<div class="media-marker" style="background:#ffebee; border-color:#f44336; color:#d32f2f;">[ MISSING FILE: ${label} ]</div>`;
                                } else if (dropDecision.action === 'dropped_text') {
                                    safeMediaInjectionHTML += `<div class="deep-fetch-container"><div class="deep-fetch-header">${dropDecision.name}</div><div class="deep-fetch-content"><pre>${escapeHTML(dropDecision.text)}</pre></div></div>`;
                                    mediaFound.push(`IMPORTED: ${dropDecision.name}`);
                                } else if (dropDecision.action === 'dropped_media') {
                                    let embedHtml = '';
                                    if (dropDecision.type.includes('video')) embedHtml = `<video controls src="${dropDecision.data}"></video>`;
                                    else if (dropDecision.type.includes('image')) embedHtml = `<img src="${dropDecision.data}"/>`;
                                    else embedHtml = `<a class="download-link" href="${dropDecision.data}" download="${dropDecision.name}">Download ${dropDecision.name}</a>`;
                                    safeMediaInjectionHTML += `<div class="deep-fetch-container"><div class="deep-fetch-header">${dropDecision.name}</div><div class="deep-fetch-content">${embedHtml}</div></div>`;
                                    mediaFound.push(`IMPORTED: ${dropDecision.name}`);
                                    dropDecision.data = null;
                                }
                            } catch (e) {
                                await closeActiveViewers();
                                mode = 'fallback';
                                reason = 'Failed video workflow.';
                                needsWizard = true;
                            }
                        } else if (wizardChoice === 'auto_read') {
                            try {
                                const clickTarget = preview.querySelector('button, a') || preview;
                                clickTarget.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));

                                const viewer = await waitForEl('.file-preview-sidebar, mat-sidenav, .mat-drawer, .mat-drawer-opened, dialog, [role="dialog"], .fullscreen-preview', 5000);
                                let extractedText = '';

                                if (viewer) {
                                    let accessible = true;
                                    let iframeDoc = null;
                                    const iframe = viewer.querySelector('iframe');
                                    if (iframe) {
                                        try {
                                            iframeDoc = iframe.contentDocument || iframe.contentWindow.document;
                                            // Reading innerText throws if cross-origin — that's our CORS gate.
                                            void iframeDoc.body.innerText;
                                        } catch (e) {
                                            accessible = false;
                                        }
                                    }

                                    if (!accessible) {
                                        mode = 'fallback';
                                        reason = 'Google restricts automatic reading (Security/CORS).';
                                        needsWizard = true;
                                    } else {
                                        const scrollTarget = iframeDoc ? (iframeDoc.scrollingElement || iframeDoc.body) : viewer;
                                        let sameHeightCount = 0;
                                        for (let s = 0; s < 25; s += 1) {
                                            const currentHeight = scrollTarget.scrollHeight;
                                            scrollTarget.scrollTop = currentHeight;
                                            await new Promise((r) => setTimeout(r, 300));
                                            if (scrollTarget.scrollHeight === currentHeight) {
                                                sameHeightCount += 1;
                                                if (sameHeightCount > 2) break;
                                            } else {
                                                sameHeightCount = 0;
                                            }
                                        }

                                        const pre = iframeDoc ? null : viewer.querySelector('pre, code, .text-content, [contenteditable="true"]');
                                        if (pre && pre.innerText.trim().length > 2) extractedText = pre.innerText;
                                        else if (iframeDoc && iframeDoc.body.innerText.length > 2) extractedText = iframeDoc.body.innerText;

                                        if (extractedText) {
                                            safeMediaInjectionHTML += `<div class="deep-fetch-container"><div class="deep-fetch-header">${label}</div><div class="deep-fetch-content"><pre>${escapeHTML(extractedText)}</pre></div></div>`;
                                            mediaFound.push(`FETCHED: ${label}`);
                                        } else {
                                            mode = 'fallback';
                                            reason = 'The file appeared empty or took too long to load.';
                                            needsWizard = true;
                                        }
                                    }
                                } else {
                                    mode = 'fallback';
                                    reason = 'Viewer did not open in time.';
                                    needsWizard = true;
                                }

                                await closeActiveViewers();
                            } catch (err) {
                                await closeActiveViewers();
                                mode = 'fallback';
                                reason = 'An error occurred reading the file.';
                                needsWizard = true;
                            }
                        }
                    }
                }

                if (stopRequested) break;

                let clone = targetContent.cloneNode(true);
                clone.querySelectorAll(filePreviewSelector).forEach((c) => c.remove());
                clone.querySelectorAll('img:not(.avatar)').forEach((imgEl) => {
                    const p = imgEl.closest('button, div');
                    if (p && p.textContent.trim() === '' && !p.querySelector('.deep-fetch-container')) p.remove();
                    else imgEl.remove();
                });

                clone.querySelectorAll(CLUTTER_SEL).forEach((j) => {
                    const parent = j.closest('div');
                    if (parent && parent.textContent.trim() === '' && !parent.querySelector('img')) parent.remove();
                    else j.remove();
                });

                if (safeMediaInjectionHTML !== '') {
                    const injectionWrapper = document.createElement('div');
                    injectionWrapper.innerHTML = safeMediaInjectionHTML;
                    clone.appendChild(injectionWrapper);
                }

                if (mediaFound.length > 0) {
                    mediaDirectory.push({
                        id: blockId,
                        snippet: rawText.substring(0, 40).replace(/\n/g, ' ') || '[Media Only]',
                        media: mediaFound
                    });
                }

                seenFingerprints.add(fingerprint);

                if (writable) {
                    await writable.write(`<div id="${blockId}" class="entry ${el.tagName || 'DIV'}">${clone.innerHTML}</div>\n`);
                }
                const elapsedMin = (Date.now() - exportStartTime) / 60000;
                const rate = elapsedMin > 0.1 ? Math.round(messageCounter / elapsedMin) : '...';
                stats.textContent = `${messageCounter} / ~${preflightCount} | ${mediaDirectory.length} media | ${rate}/min`;
                log(`Block ${messageCounter} Written.`);
                el.style.outline = '';
                el.dataset.v45Captured = 'true';

                clone = null;
                safeMediaInjectionHTML = null;
            }

            if (stopRequested) break;
            if (newMessagesThisLoop === 0) stuckCounter += 1;
            else stuckCounter = 0;

            if (scroller !== window) scroller.scrollTop += 400;
            else window.scrollBy({ top: 400, behavior: 'smooth' });
            await new Promise((r) => setTimeout(r, 400));
        }

        if (stopRequested) {
            if (writable) {
                try {
                    // Write minimal closing tags so the output file is valid HTML.
                    await writable.write('<p style="color:red; text-align:center; padding:20px;">[ Export was terminated early ]</p></body></html>');
                    await writable.close();
                } catch (e) {
                    // no-op
                }
            }
            await releaseAntiThrottling();
            exportDiagnosticLogs('terminated-early');
            return;
        }

        log('Finalizing Appendix...');
        let appendix = '<div id="media-appendix" class="map-box"><h2>Appendix: Media Map Checklist</h2>';

        if (missingFiles.size > 0) {
            appendix += '<div style="background:#ffebee; border:1px solid #f44336; padding:15px; border-radius:8px; margin-bottom:20px;"><h3 style="color:#d32f2f; margin-top:0;">Missing Files (Manual Download Required)</h3><ul style="margin-bottom:0; color:#b71c1c; font-weight:bold;">';
            Array.from(missingFiles.values()).forEach((doc) => {
                appendix += `<li>${escapeHTML(doc.label)} ${doc.link ? `<a href="${doc.link}" target="_blank">[Drive Link]</a>` : ''}</li>`;
            });
            appendix += '</ul></div>';
        }

        if (mediaDirectory.length === 0) {
            appendix += '<p>No media detected.</p>';
        } else {
            mediaDirectory.forEach((item) => {
                appendix += `<div style="padding:10px 0; border-bottom:1px solid #eee;"><span><strong>${escapeHTML(item.snippet)}...</strong><br><small style="color:#d32f2f;font-weight:bold;">${item.media.map(escapeHTML).join(' | ')}</small></span><a href="#${item.id}" class="jump">JUMP TO MESSAGE</a></div>`;
            });
        }

        if (writable) {
            await writable.write(`${appendix}</div></body></html>`);
            await writable.close();
        }

        log('Export Complete!');
        await releaseAntiThrottling();
        exportDiagnosticLogs('completed');

        btnContainer.innerHTML = '';
        const reloadBtn = document.createElement('button');
        reloadBtn.textContent = 'COMPLETE (Click to Reload)';
        Object.assign(reloadBtn.style, {
            flex: '1',
            padding: '15px',
            background: '#34A853',
            border: 'none',
            color: '#fff',
            borderRadius: '4px',
            cursor: 'pointer',
            fontWeight: 'bold'
        });
        reloadBtn.onclick = () => window.location.reload();
        btnContainer.appendChild(reloadBtn);
    };
}
