import { ConnectionFactory } from "./websocket";
import { WebTTY, protocols } from "./webtty";
import { GoTTYXterm } from "./xterm";
import { createIdleAlert } from "./idle-alert";
import { installFaviconAlert } from "./favicon-alert";
import { VoiceInput } from "./voice-input";
import { initThemePicker } from "./theme-picker";
import { initShareManager } from "./share-manager";

// @TODO remove these
declare var gotty_auth_token: string;
declare var gotty_term: string;
declare var gotty_ws_query_args: string;
declare var gotty_permit_write: boolean;
declare var gotty_enable_idle_alert: boolean;
declare var gotty_idle_alert_timeout: number;
declare var gotty_enable_asr: boolean;
declare var gotty_asr_hold_ms: number;
declare var gotty_asr_hotkey: string;
declare var gotty_preferences: Record<string, unknown>;
declare var gotty_share_enabled: boolean;

// Helper function to get cookie value
function getCookie(name: string): string | null {
    const value = `; ${document.cookie}`;
    const parts = value.split(`; ${name}=`);
    if (parts.length === 2) return parts.pop()?.split(';').shift() || null;
    return null;
}

function waitForServerRestart(term: GoTTYXterm, basePath: string): void {
    const startedAt = Date.now();
    let sawServerDown = false;

    const poll = async () => {
        try {
            const response = await fetch(`${basePath}config.js?restart=${Date.now()}`, {
                cache: "no-store",
                credentials: "same-origin",
            });
            if (response.ok && (sawServerDown || Date.now() - startedAt > 1500)) {
                window.location.reload();
                return;
            }
        } catch (_) {
            sawServerDown = true;
        }

        if (Date.now() - startedAt > 60000) {
            term.showMessage("Restart request was sent, but the server did not come back automatically.", 0);
            return;
        }
        window.setTimeout(poll, 1000);
    };

    window.setTimeout(poll, 800);
}

function installServerRestart(term: GoTTYXterm, basePath: string, authToken: string): void {
    term.setRestartSender(async () => {
        try {
            const headers: Record<string, string> = {
                "X-GoTTY-Action": "restart",
            };
            if (authToken) {
                headers.Authorization = `Bearer ${authToken}`;
            }
            const response = await fetch(`${basePath}-/restart`, {
                method: "POST",
                credentials: "same-origin",
                headers,
            });
            if (!response.ok) {
                let message = `HTTP ${response.status}`;
                try {
                    const payload = await response.json();
                    if (payload && typeof payload.message === "string") {
                        message = payload.message;
                    }
                } catch (_) {
                    // Keep the status-based message.
                }
                throw new Error(message);
            }
            term.showMessage("Restart request sent. Waiting for GoTTY to come back...", 0);
            waitForServerRestart(term, basePath);
        } catch (err) {
            const message = err instanceof Error ? err.message : String(err);
            term.showMessage(`Restart failed: ${message}`, 5000);
        }
    });
}

// Get auth token with fallback chain: localStorage -> Cookie -> global variable
let authToken: string = '';
try {
    authToken = localStorage.getItem('gotty_auth_token') ||
                getCookie('gotty_auth_token') ||
                (typeof gotty_auth_token !== 'undefined' ? gotty_auth_token : '');

    // Save to localStorage for persistence
    if (authToken && authToken !== localStorage.getItem('gotty_auth_token')) {
        localStorage.setItem('gotty_auth_token', authToken);
    }
} catch (e) {
    // Fallback if localStorage is not available
    authToken = getCookie('gotty_auth_token') ||
                (typeof gotty_auth_token !== 'undefined' ? gotty_auth_token : '');
}

const elem = document.getElementById("terminal")

if (elem !== null) {
    var term: GoTTYXterm;
    term = new GoTTYXterm(elem, gotty_preferences);
    initThemePicker(term.term);
    initShareManager();

    const subscribeTermActivity = (cb: () => void) => {
        const unsubOutput = term.onOutput(cb);
        const unsubInput = term.onInputActivity(cb);
        const unsubSelection = term.onSelectionActivity(cb);

        const onWheel = () => cb();
        const onKeydown = () => cb();
        term.elem.addEventListener('wheel', onWheel, { passive: true });
        term.elem.addEventListener('keydown', onKeydown, true);

        return () => {
            unsubOutput();
            unsubInput();
            unsubSelection();
            term.elem.removeEventListener('wheel', onWheel);
            term.elem.removeEventListener('keydown', onKeydown, true);
        };
    };

    const timeoutSeconds =
        (typeof gotty_idle_alert_timeout === 'number' && gotty_idle_alert_timeout > 0)
            ? gotty_idle_alert_timeout
            : 30;

    const faviconStopTimeoutMs = 6000;
    const uninstallFaviconAlert = installFaviconAlert({
        stopTimeoutMs: faviconStopTimeoutMs,
        onActivity: subscribeTermActivity,
    });

    // 如果启用了空闲提醒功能，创建组件
    if (typeof gotty_enable_idle_alert !== 'undefined' && gotty_enable_idle_alert) {
        const alertContainer = document.createElement('div');
        alertContainer.id = 'idle-alert-container';
        document.body.appendChild(alertContainer);

        createIdleAlert(
            alertContainer,
            timeoutSeconds,
            subscribeTermActivity
        );
    }

    const httpsEnabled = window.location.protocol == "https:";
    const queryArgs = (gotty_ws_query_args === "") ? "" : "?" + gotty_ws_query_args;
    const basePath = window.location.pathname.endsWith('/') ? window.location.pathname : window.location.pathname + '/';
    const url = (httpsEnabled ? 'wss://' : 'ws://') + window.location.host + basePath + 'ws' + queryArgs;
    const args = window.location.search;
    const factory = new ConnectionFactory(url, protocols);
    const wt = new WebTTY(term, factory, args, authToken);
    installServerRestart(term, basePath, authToken);
    const closer = wt.open();

    let voiceInput: VoiceInput | null = null;
    if (typeof gotty_enable_asr !== 'undefined' && gotty_enable_asr) {
        voiceInput = new VoiceInput({
            term,
            authToken: authToken,
            enabled: gotty_enable_asr,
            permitWrite: typeof gotty_permit_write !== 'undefined' ? gotty_permit_write : false,
            holdMs: (typeof gotty_asr_hold_ms === 'number' && gotty_asr_hold_ms >= 0) ? gotty_asr_hold_ms : 500,
            hotkeyCode: (typeof gotty_asr_hotkey === 'string' && gotty_asr_hotkey) ? gotty_asr_hotkey : 'ShiftRight',
        });
    }

    // According to https://developer.mozilla.org/en-US/docs/Web/API/Window/unload_event
    // this event is unreliable and in some cases (Firefox is mentioned), having an
    // "unload" event handler can have unwanted side effects. Consider commenting it out.
    window.addEventListener("unload", () => {
        uninstallFaviconAlert();
        voiceInput?.close();
        closer();
        term.close();
    });

    // Listen for focus request from parent window (e.g., Miao tab switching)
    window.addEventListener("message", (event) => {
        const data = event.data;
        if (data && data.type === 'gotty-focus') {
            term.focus();
        }
    });
};
