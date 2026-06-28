declare var gotty_share_enabled: boolean;

type ShareStatus = "creating" | "active" | "expired" | "stopped" | "failed" | "lost";

interface ShareRecord {
    id: string;
    type: "http" | "tcp";
    target: string;
    public_url: string;
    status: ShareStatus;
    created_at: string;
    expires_at: string;
    stopped_at?: string;
    last_error?: string;
    is_terminal?: boolean;
}

interface ShareListResponse {
    shares: ShareRecord[];
    default_target: string;
    enabled: boolean;
}

const TTL_OPTIONS = [
    { label: "15 min", value: 900 },
    { label: "1 hour", value: 3600 },
    { label: "4 hours", value: 14400 },
];

const SHARE_TOOLBAR_OFFSET = 32;
const TOOLBAR_BASE_TOPS = [
    { selector: ".clear-history-btn", top: 74 },
    { selector: ".upload-btn", top: 106 },
    { selector: ".restart-btn", top: 138 },
    { selector: ".terminal-state", top: 170 },
];

function basePath(): string {
    return window.location.pathname.endsWith("/") ? window.location.pathname : window.location.pathname + "/";
}

function shareAPI(path: string): string {
    return basePath() + path.replace(/^\//, "");
}

function statusClass(status: ShareStatus, expiresAt: string): string {
    if (status === "active") {
        const remaining = new Date(expiresAt).getTime() - Date.now();
        return remaining > 0 && remaining < 5 * 60 * 1000 ? "expiring" : "active";
    }
    return status;
}

function relativeTime(value: string): string {
    const ts = new Date(value).getTime();
    if (!ts) return "";
    const diff = ts - Date.now();
    const abs = Math.abs(diff);
    const suffix = diff >= 0 ? "left" : "ago";
    if (abs < 60 * 1000) return `${Math.max(1, Math.round(abs / 1000))}s ${suffix}`;
    if (abs < 60 * 60 * 1000) return `${Math.round(abs / 60000)}m ${suffix}`;
    if (abs < 24 * 60 * 60 * 1000) return `${Math.round(abs / 3600000)}h ${suffix}`;
    return `${Math.round(abs / 86400000)}d ${suffix}`;
}

function shareIcon(): string {
    return '<svg width="14" height="14" viewBox="0 0 16 16" fill="none" xmlns="http://www.w3.org/2000/svg"><path d="M10.5 3.5L12.5 5.5L10.5 7.5M12.5 5.5H7.5A4 4 0 003.5 9.5V12.5M5.5 5.5H3.5V13.5H11.5V11.5" stroke="rgba(255,255,255,0.7)" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round"/></svg>';
}

function setToolbarShareSlot(enabled: boolean): void {
    const offset = enabled ? SHARE_TOOLBAR_OFFSET : 0;
    for (const item of TOOLBAR_BASE_TOPS) {
        const element = document.querySelector<HTMLElement>(item.selector);
        if (element) {
            element.style.top = `${item.top + offset}px`;
        }
    }
}

function installShareStyles(): void {
    if (document.getElementById("gotty-share-styles")) return;
    const style = document.createElement("style");
    style.id = "gotty-share-styles";
    style.textContent = `
#gotty-share-btn {
    position: fixed;
    top: 74px;
    right: 10px;
    width: 28px;
    height: 28px;
    border-radius: 4px;
    border: none;
    background: rgba(0,0,0,0.7);
    color: #fff;
    padding: 0;
    cursor: pointer;
    z-index: 1000;
    display: flex;
    align-items: center;
    justify-content: center;
    line-height: 1;
}
#gotty-share-btn:hover {
    background: rgba(0,0,0,0.75);
}
#gotty-share-panel {
    position: fixed;
    top: 74px;
    right: 46px;
    z-index: 9999;
    display: none;
    min-width: 320px;
    max-width: min(420px, calc(100vw - 64px));
    max-height: calc(100vh - 86px);
    overflow-y: auto;
}
#gotty-share-panel.open {
    display: block;
}
#gotty-share-panel::-webkit-scrollbar {
    width: 4px;
}
#gotty-share-panel::-webkit-scrollbar-track {
    background: transparent;
}
#gotty-share-panel::-webkit-scrollbar-thumb {
    background: rgba(255,255,255,0.15);
    border-radius: 2px;
}
#gotty-share-panel .panel {
    background: rgba(30,30,30,0.92);
    backdrop-filter: blur(8px);
    border: 1px solid rgba(255,255,255,0.12);
    border-radius: 10px;
    padding: 6px;
    box-shadow: 0 8px 24px rgba(0,0,0,0.5);
    color: #ddd;
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
}
#gotty-share-panel .section-title {
    font-size: 10px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.8px;
    color: rgba(255,255,255,0.35);
    padding: 10px 10px 6px;
}
#gotty-share-panel .section-title:first-child {
    padding-top: 4px;
}
#gotty-share-panel .section-divider {
    height: 1px;
    background: rgba(255,255,255,0.08);
    margin: 6px 10px;
}
#gotty-share-panel .share-form {
    padding: 0 10px 8px;
    display: grid;
    gap: 6px;
}
#gotty-share-panel input,
#gotty-share-panel select {
    width: 100%;
    height: 28px;
    border: 1px solid rgba(255,255,255,0.12);
    border-radius: 5px;
    background: rgba(0,0,0,0.25);
    color: #ddd;
    font-size: 12px;
    padding: 0 8px;
    box-sizing: border-box;
}
#gotty-share-panel .form-row {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 6px;
}
#gotty-share-panel .share-primary,
#gotty-share-panel .share-small {
    border: 1px solid rgba(255,255,255,0.12);
    border-radius: 5px;
    background: rgba(255,255,255,0.1);
    color: #eee;
    cursor: pointer;
    font-size: 12px;
    height: 28px;
    padding: 0 8px;
}
#gotty-share-panel .share-primary:hover,
#gotty-share-panel .share-small:hover {
    background: rgba(255,255,255,0.16);
}
#gotty-share-panel .share-primary:disabled,
#gotty-share-panel .share-small:disabled {
    opacity: 0.45;
    cursor: default;
}
#gotty-share-panel .share-record {
    display: grid;
    grid-template-columns: 8px 1fr;
    gap: 8px;
    padding: 8px 10px;
    border-radius: 6px;
    color: #ccc;
    font-size: 12px;
}
#gotty-share-panel .share-record:hover {
    background: rgba(255,255,255,0.08);
}
#gotty-share-panel .status-dot {
    width: 7px;
    height: 7px;
    margin-top: 5px;
    border-radius: 50%;
    background: rgba(255,255,255,0.35);
}
#gotty-share-panel .status-dot.active { background: #4ade80; }
#gotty-share-panel .status-dot.expiring,
#gotty-share-panel .status-dot.lost { background: #facc15; }
#gotty-share-panel .status-dot.failed { background: #f87171; }
#gotty-share-panel .share-url,
#gotty-share-panel .share-meta,
#gotty-share-panel .share-error {
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
}
#gotty-share-panel .share-url {
    color: #fff;
    font-size: 13px;
}
#gotty-share-panel .share-meta {
    color: rgba(255,255,255,0.45);
    margin-top: 2px;
}
#gotty-share-panel .share-error {
    color: #fca5a5;
    margin-top: 2px;
}
#gotty-share-panel .share-actions {
    display: flex;
    gap: 5px;
    margin-top: 7px;
    flex-wrap: wrap;
}
#gotty-share-panel .share-action {
    border: 1px solid rgba(255,255,255,0.1);
    border-radius: 5px;
    background: transparent;
    color: #bbb;
    cursor: pointer;
    font-size: 11px;
    height: 24px;
    padding: 0 7px;
}
#gotty-share-panel .share-action:hover {
    background: rgba(255,255,255,0.1);
    color: #fff;
}
#gotty-share-panel .share-empty,
#gotty-share-panel .share-status {
    color: rgba(255,255,255,0.45);
    padding: 8px 10px;
    font-size: 12px;
}
`;
    document.head.appendChild(style);
}

export function initShareManager(): void {
    if (typeof gotty_share_enabled === "undefined" || !gotty_share_enabled) {
        return;
    }

    installShareStyles();
    setToolbarShareSlot(true);

    const btn = document.createElement("button");
    btn.id = "gotty-share-btn";
    btn.type = "button";
    btn.innerHTML = shareIcon();
    btn.title = "Share manager";
    btn.setAttribute("aria-label", "Share manager");

    const container = document.createElement("div");
    container.id = "gotty-share-panel";
    const panel = document.createElement("div");
    panel.className = "panel";
    container.appendChild(panel);

    let shares: ShareRecord[] = [];
    let defaultTarget = "";
    let loading = false;
    let statusMessage = "";

    const requestJSON = async (path: string, init?: RequestInit) => {
        const res = await fetch(shareAPI(path), {
            ...init,
            headers: {
                "Content-Type": "application/json",
                ...(init?.headers || {}),
            },
        });
        if (!res.ok) {
            const body = await res.json().catch(() => ({}));
            throw new Error(body.message || res.statusText);
        }
        return res.json();
    };

    const refresh = async () => {
        const data = await requestJSON("/-/shares") as ShareListResponse;
        shares = data.shares || [];
        defaultTarget = data.default_target || defaultTarget;
        render();
    };

    const copyText = async (text: string) => {
        await navigator.clipboard?.writeText(text);
        statusMessage = "Copied";
        render();
        setTimeout(() => {
            statusMessage = "";
            render();
        }, 1200);
    };

    const createShare = async (target: string, type: string, ttl: number) => {
        loading = true;
        statusMessage = "Creating share...";
        render();
        try {
            const record = await requestJSON("/-/share", {
                method: "POST",
                body: JSON.stringify({ target, type, ttl_seconds: ttl }),
            }) as ShareRecord;
            statusMessage = "Share created";
            if (record.public_url) {
                copyText(record.public_url).catch(() => undefined);
            }
            await refresh();
        } catch (err) {
            statusMessage = err instanceof Error ? err.message : "Failed to create share";
            render();
        } finally {
            loading = false;
            render();
        }
    };

    const stopShare = async (id: string) => {
        await requestJSON(`/-/shares/${encodeURIComponent(id)}`, { method: "DELETE" });
        await refresh();
    };

    const restartShare = async (id: string) => {
        loading = true;
        statusMessage = "Restarting share...";
        render();
        try {
            await requestJSON(`/-/shares/${encodeURIComponent(id)}/restart`, { method: "POST" });
            await refresh();
            statusMessage = "";
        } catch (err) {
            statusMessage = err instanceof Error ? err.message : "Failed to restart share";
            render();
        } finally {
            loading = false;
            render();
        }
    };

    const deleteRecord = async (id: string) => {
        await requestJSON(`/-/shares/${encodeURIComponent(id)}/record`, { method: "DELETE" });
        await refresh();
    };

    const addTitle = (text: string) => {
        const title = document.createElement("div");
        title.className = "section-title";
        title.textContent = text;
        panel.appendChild(title);
    };

    const addDivider = () => {
        const divider = document.createElement("div");
        divider.className = "section-divider";
        panel.appendChild(divider);
    };

    const renderRecord = (record: ShareRecord) => {
        const row = document.createElement("div");
        row.className = "share-record";

        const dot = document.createElement("div");
        dot.className = `status-dot ${statusClass(record.status, record.expires_at)}`;
        row.appendChild(dot);

        const body = document.createElement("div");
        const url = document.createElement("div");
        url.className = "share-url";
        url.title = record.public_url;
        url.textContent = record.public_url || "(no public URL)";
        body.appendChild(url);

        const meta = document.createElement("div");
        meta.className = "share-meta";
        const statusText = record.status === "active" ? relativeTime(record.expires_at) : record.status;
        meta.textContent = `${record.target} · ${statusText}`;
        meta.title = record.target;
        body.appendChild(meta);

        if (record.last_error) {
            const error = document.createElement("div");
            error.className = "share-error";
            error.title = record.last_error;
            error.textContent = record.last_error;
            body.appendChild(error);
        }

        const actions = document.createElement("div");
        actions.className = "share-actions";

        const copy = actionButton("Copy", () => copyText(record.public_url));
        actions.appendChild(copy);

        if (record.status === "active") {
            if (record.public_url.startsWith("http")) {
                actions.appendChild(actionButton("Open", () => window.open(record.public_url, "_blank", "noopener")));
            }
            actions.appendChild(actionButton("Stop", () => stopShare(record.id)));
        } else {
            actions.appendChild(actionButton("Restart", () => restartShare(record.id)));
            actions.appendChild(actionButton("Delete", () => deleteRecord(record.id)));
        }

        body.appendChild(actions);
        row.appendChild(body);
        panel.appendChild(row);
    };

    const actionButton = (text: string, onClick: () => void) => {
        const button = document.createElement("button");
        button.type = "button";
        button.className = "share-action";
        button.textContent = text;
        button.disabled = loading;
        button.addEventListener("click", (event) => {
            event.stopPropagation();
            onClick();
        });
        return button;
    };

    const render = () => {
        panel.innerHTML = "";

        addTitle("Share");
        const form = document.createElement("div");
        form.className = "share-form";

        const quick = document.createElement("button");
        quick.type = "button";
        quick.className = "share-primary";
        quick.textContent = "Share this terminal";
        quick.disabled = loading || !defaultTarget;
        quick.addEventListener("click", () => createShare("", "http", Number(ttlSelect.value)));
        form.appendChild(quick);

        const target = document.createElement("input");
        target.type = "text";
        target.placeholder = "host:port";
        target.value = defaultTarget;
        form.appendChild(target);

        const row = document.createElement("div");
        row.className = "form-row";

        const typeSelect = document.createElement("select");
        for (const option of [{ label: "HTTP/WebSocket", value: "http" }, { label: "TCP", value: "tcp" }]) {
            const item = document.createElement("option");
            item.value = option.value;
            item.textContent = option.label;
            typeSelect.appendChild(item);
        }
        row.appendChild(typeSelect);

        const ttlSelect = document.createElement("select");
        for (const option of TTL_OPTIONS) {
            const item = document.createElement("option");
            item.value = String(option.value);
            item.textContent = option.label;
            ttlSelect.appendChild(item);
        }
        row.appendChild(ttlSelect);
        form.appendChild(row);

        const create = document.createElement("button");
        create.type = "button";
        create.className = "share-primary";
        create.textContent = loading ? "Working..." : "Create share";
        create.disabled = loading;
        create.addEventListener("click", () => createShare(target.value, typeSelect.value, Number(ttlSelect.value)));
        form.appendChild(create);

        panel.appendChild(form);

        if (statusMessage) {
            const status = document.createElement("div");
            status.className = "share-status";
            status.textContent = statusMessage;
            panel.appendChild(status);
        }

        addDivider();
        addTitle("Active");
        const active = shares.filter((s) => s.status === "active");
        if (active.length === 0) {
            const empty = document.createElement("div");
            empty.className = "share-empty";
            empty.textContent = "No active shares";
            panel.appendChild(empty);
        } else {
            active.forEach(renderRecord);
        }

        addDivider();
        addTitle("History");
        const history = shares.filter((s) => s.status !== "active");
        if (history.length === 0) {
            const empty = document.createElement("div");
            empty.className = "share-empty";
            empty.textContent = "No share history";
            panel.appendChild(empty);
        } else {
            history.forEach(renderRecord);
        }
    };

    const openPanel = () => {
        container.classList.add("open");
        refresh().catch((err) => {
            statusMessage = err instanceof Error ? err.message : "Failed to load shares";
            render();
        });
    };

    const togglePanel = () => {
        if (container.classList.contains("open")) {
            container.classList.remove("open");
            return;
        }
        openPanel();
    };

    btn.addEventListener("click", (event) => {
        event.stopPropagation();
        togglePanel();
    });

    const connectionCount = document.querySelector<HTMLElement>(".connection-count");
    if (connectionCount) {
        connectionCount.style.cursor = "pointer";
        connectionCount.addEventListener("click", (event) => {
            event.stopPropagation();
            togglePanel();
        });
    }

    document.addEventListener("click", (event) => {
        const target = event.target as Node;
        if (!container.contains(target) && target !== btn && target !== connectionCount) {
            container.classList.remove("open");
        }
    });

    document.body.appendChild(btn);
    document.body.appendChild(container);

    refresh().catch((err) => {
        statusMessage = err instanceof Error ? err.message : "Failed to load shares";
        render();
    });
    setInterval(() => {
        if (container.classList.contains("open")) {
            refresh().catch(() => undefined);
        }
    }, 10000);
}
