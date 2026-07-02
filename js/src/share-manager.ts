declare var gotty_share_enabled: boolean;
declare var gotty_share_public_host: boolean;
declare var gotty_share_default_target: string;

type ShareStatus = "creating" | "active" | "expired" | "stopped" | "failed" | "lost";

interface ShareRecord {
    id: string;
    type: "http";
    target: string;
    subdomain?: string;
    public_url: string;
    status: ShareStatus;
    created_at: string;
    expires_at?: string;
    ttl_seconds?: number;
    stopped_at?: string;
    last_error?: string;
    is_terminal?: boolean;
}

interface ShareListResponse {
    shares: ShareRecord[];
    default_target: string;
    enabled: boolean;
    configured?: boolean;
    missing_config?: string[];
    public_domain?: string;
    subdomain_prefix?: string;
}

const EXPIRY_UNITS = [
    { label: "Minutes", value: "minutes" },
    { label: "Hours", value: "hours" },
    { label: "Days", value: "days" },
];

function basePath(): string {
    return window.location.pathname.endsWith("/") ? window.location.pathname : window.location.pathname + "/";
}

function shareAPI(path: string): string {
    return basePath() + path.replace(/^\//, "");
}

function statusClass(status: ShareStatus, expiresAt?: string): string {
    if (status === "active") {
        if (!expiresAt) return "active";
        const remaining = new Date(expiresAt).getTime() - Date.now();
        return remaining > 0 && remaining < 5 * 60 * 1000 ? "expiring" : "active";
    }
    return status;
}

function relativeTime(value?: string): string {
    if (!value) return "Never expires";
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

function normalizeSuffix(value: string): string {
    return value.trim().toLowerCase().replace(/^gotty-/, "");
}

function installShareStyles(): void {
    if (document.getElementById("gotty-share-styles")) return;
    const style = document.createElement("style");
    style.id = "gotty-share-styles";
    style.textContent = `
#gotty-share-panel {
    position: fixed;
    top: 42px;
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
    border-radius: 8px;
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
#gotty-share-panel input::placeholder {
    color: rgba(255,255,255,0.35);
}
#gotty-share-panel .form-row {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 6px;
}
#gotty-share-panel .expiry-row {
    grid-template-columns: 1fr 112px;
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
#gotty-share-panel .share-preview {
    color: rgba(255,255,255,0.42);
    font-size: 11px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
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
    if (typeof gotty_share_public_host !== "undefined" && gotty_share_public_host) {
        return;
    }

    installShareStyles();

    const enabled = typeof gotty_share_enabled !== "undefined" && gotty_share_enabled;
    const container = document.createElement("div");
    container.id = "gotty-share-panel";
    const panel = document.createElement("div");
    panel.className = "panel";
    container.appendChild(panel);

    const initialDefaultTarget = typeof gotty_share_default_target !== "undefined" ? gotty_share_default_target : "";
    let shares: ShareRecord[] = [];
    let defaultTarget = initialDefaultTarget;
    let publicDomain = "httptunnel.top";
    let subdomainPrefix = "gotty-";
    let loading = false;
    let statusMessage = "";
    let shareConfigured = false;
    let missingConfig: string[] = [];
    let targetValue = initialDefaultTarget;
    let targetTouched = false;
    let subdomainValue = "";
    let expireValue = "";
    let expireUnit = "hours";

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
        publicDomain = data.public_domain || publicDomain;
        subdomainPrefix = data.subdomain_prefix || subdomainPrefix;
        if (!targetTouched && defaultTarget) {
            targetValue = defaultTarget;
        }
        shareConfigured = !!data.configured;
        missingConfig = data.missing_config || [];
        if (!shareConfigured) {
            statusMessage = `HTTP tunnel settings incomplete: ${missingConfig.join(", ")}`;
        } else if (statusMessage.startsWith("HTTP tunnel settings incomplete:")) {
            statusMessage = "";
        }
        render();
    };

    const copyText = async (text: string) => {
        if (!text) return;
        await navigator.clipboard?.writeText(text);
        statusMessage = "Copied";
        render();
        setTimeout(() => {
            statusMessage = "";
            render();
        }, 1200);
    };

    const expiryPayload = () => {
        const value = expireValue.trim();
        if (!value) {
            return { expire_value: 0, expire_unit: "" };
        }
        if (!/^[1-9][0-9]*$/.test(value)) {
            throw new Error("Expiry must be a positive integer");
        }
        return { expire_value: Number(value), expire_unit: expireUnit };
    };

    const createShare = async (target: string) => {
        loading = true;
        statusMessage = "Creating share...";
        render();
        try {
            const expiry = expiryPayload();
            const record = await requestJSON("/-/share", {
                method: "POST",
                body: JSON.stringify({
                    target,
                    type: "http",
                    subdomain: subdomainValue.trim(),
                    ...expiry,
                }),
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
        try {
            await requestJSON(`/-/shares/${encodeURIComponent(id)}`, { method: "DELETE" });
            await refresh();
        } catch (err) {
            statusMessage = err instanceof Error ? err.message : "Failed to stop share";
            render();
        }
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
        try {
            await requestJSON(`/-/shares/${encodeURIComponent(id)}/record`, { method: "DELETE" });
            await refresh();
        } catch (err) {
            statusMessage = err instanceof Error ? err.message : "Failed to delete share";
            render();
        }
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

        actions.appendChild(actionButton("Copy", () => copyText(record.public_url)));

        if (record.status === "active") {
            actions.appendChild(actionButton("Open", () => window.open(record.public_url, "_blank", "noopener")));
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

        const target = document.createElement("input");
        target.type = "text";
        target.placeholder = "localhost:8080";
        target.value = targetValue || defaultTarget;
        target.setAttribute("aria-label", "Target host and port");
        target.addEventListener("input", () => {
            targetTouched = true;
            targetValue = target.value;
        });
        form.appendChild(target);

        const subdomain = document.createElement("input");
        subdomain.type = "text";
        subdomain.placeholder = "subdomain suffix (optional)";
        subdomain.value = subdomainValue;
        subdomain.setAttribute("aria-label", "Subdomain suffix");
        subdomain.addEventListener("input", () => {
            subdomainValue = subdomain.value;
        });
        form.appendChild(subdomain);

        const row = document.createElement("div");
        row.className = "form-row expiry-row";

        const expiry = document.createElement("input");
        expiry.type = "number";
        expiry.min = "1";
        expiry.step = "1";
        expiry.placeholder = "Never expires";
        expiry.value = expireValue;
        expiry.setAttribute("aria-label", "Expiry value");
        expiry.addEventListener("input", () => {
            expireValue = expiry.value;
        });
        row.appendChild(expiry);

        const unitSelect = document.createElement("select");
        unitSelect.setAttribute("aria-label", "Expiry unit");
        for (const option of EXPIRY_UNITS) {
            const item = document.createElement("option");
            item.value = option.value;
            item.textContent = option.label;
            item.selected = option.value === expireUnit;
            unitSelect.appendChild(item);
        }
        unitSelect.addEventListener("change", () => {
            expireUnit = unitSelect.value;
        });
        row.appendChild(unitSelect);
        form.appendChild(row);

        const preview = document.createElement("div");
        preview.className = "share-preview";
        const suffix = normalizeSuffix(subdomainValue);
        preview.textContent = suffix
            ? `https://${subdomainPrefix}${suffix}.${publicDomain}`
            : `https://${subdomainPrefix}xxxxxx.${publicDomain}`;
        preview.title = preview.textContent;
        form.appendChild(preview);

        const create = document.createElement("button");
        create.type = "button";
        create.className = "share-primary";
        create.textContent = loading ? "Working..." : (shareConfigured ? "Create share" : "Configure HTTP tunnel first");
        create.disabled = loading || !shareConfigured;
        create.addEventListener("click", () => createShare(target.value));
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

    const renderDisabled = () => {
        panel.innerHTML = "";
        addTitle("Share");
        const status = document.createElement("div");
        status.className = "share-status";
        status.textContent = "Share management is not enabled. Start gotty with --share-enabled to configure sharing.";
        panel.appendChild(status);
    };

    const openPanel = () => {
        container.classList.add("open");
        if (!enabled) {
            renderDisabled();
            return;
        }
        statusMessage = "Loading shares...";
        render();
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

    const connectionCount = document.querySelector<HTMLElement>(".connection-count");
    if (connectionCount) {
        connectionCount.style.cursor = "pointer";
        connectionCount.title = "Connections / Share manager";
        connectionCount.addEventListener("click", (event) => {
            event.stopPropagation();
            togglePanel();
        });
    }

    document.addEventListener("click", (event) => {
        const target = event.target as Node;
        if (!container.contains(target) && !connectionCount?.contains(target)) {
            container.classList.remove("open");
        }
    });

    document.body.appendChild(container);

    if (enabled) {
        refresh().catch((err) => {
            statusMessage = err instanceof Error ? err.message : "Failed to load shares";
            render();
        });
    } else {
        renderDisabled();
    }
    setInterval(() => {
        if (enabled && container.classList.contains("open")) {
            refresh().catch(() => undefined);
        }
    }, 10000);
}
