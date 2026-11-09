// Local management UI. Device credentials stay in the GoTTY backend.
declare var gotty_hub_enabled: boolean;
type HubState = { url: string; username: string; instance_id: string; online: boolean };
const PANEL_OPEN_EVENT = 'gotty-panel-open';

export function initHubManager(basePath: string, authToken: string): () => void {
    if (typeof gotty_hub_enabled === 'undefined' || !gotty_hub_enabled) return () => {};
    const style = document.createElement('style');
    style.textContent = `
#gotty-hub-btn {position:fixed;top:170px;right:10px;width:28px;height:28px;border-radius:4px;border:none;background:rgba(0,0,0,.7);color:#fff;padding:0;cursor:pointer;z-index:1000;display:flex;align-items:center;justify-content:center;line-height:1}
#gotty-hub-btn:hover {background:rgba(0,0,0,.75)}
#gotty-hub-btn:focus-visible {outline:2px solid #9aa0a6;outline-offset:2px}
#gotty-hub-dialog {color:#eee;background:#222;border:1px solid #555;border-radius:8px;width:calc(100% - 32px);max-width:440px;max-height:calc(100dvh - 32px);overflow:auto;padding:24px;font:14px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}
#gotty-hub-dialog::backdrop {background:rgba(0,0,0,.5)}
#gotty-hub-dialog h2 {font-size:18px;margin:0 0 16px}
#gotty-hub-dialog form {display:flex;flex-direction:column;gap:12px}
#gotty-hub-dialog label {display:flex;flex-direction:column;gap:6px}
#gotty-hub-dialog input {width:100%;min-width:0;box-sizing:border-box;background:#333;border:1px solid #666;border-radius:4px;padding:8px 10px;color:inherit;font:inherit}
#gotty-hub-dialog input:focus-visible {outline:2px solid #9aa0a6;outline-offset:1px}
#gotty-hub-dialog p {margin:0;overflow-wrap:anywhere}
#gotty-hub-dialog .hub-actions {display:flex;justify-content:flex-end;gap:8px}
#gotty-hub-dialog button {font:inherit;background:#444;color:inherit;border:1px solid #666;border-radius:4px;padding:6px 12px;cursor:pointer}
#gotty-hub-dialog button:hover {background:#555}
#gotty-hub-dialog button:disabled {opacity:.5;cursor:wait}
#gotty-hub-dialog a {color:#8ab4f8}
#gotty-hub-dialog [role=alert] {color:#f28b82}
`;
    document.head.append(style);
    const button = document.createElement('button');
    button.id = 'gotty-hub-btn'; button.type = 'button';
    button.title = '接入 GoTTY Hub'; button.setAttribute('aria-label', button.title);
    button.innerHTML = '<svg width="14" height="14" viewBox="0 0 16 16" fill="none" aria-hidden="true"><path d="M6 5.5H4.5a2.5 2.5 0 000 5H6m4-5h1.5a2.5 2.5 0 010 5H10m-5-2h6" stroke="rgba(255,255,255,.7)" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round"/></svg>';
    document.body.append(button);
    const terminalState = document.querySelector<HTMLElement>('.terminal-state');
    const previousTop = terminalState?.style.top;
    if (terminalState) terminalState.style.top = '202px';
    const headers = () => ({ 'Content-Type': 'application/json', ...(authToken ? { Authorization: `Bearer ${authToken}` } : {}) });
    const view = Array.from(crypto.getRandomValues(new Uint8Array(32)), b => b.toString(16).padStart(2, '0')).join('');
    let dialog: HTMLDialogElement | undefined;
    let connection: HTMLParagraphElement | undefined;
    let submitting = false;
    let syncing = false;
    let disposed = false;
    let requestController: AbortController | undefined;

    async function state(): Promise<HubState> {
        const response = await fetch(basePath + '-/hub', { headers: headers(), cache: 'no-store' });
        if (!response.ok) throw new Error('无法读取 Hub 配置，请检查 GoTTY 登录状态。');
        return response.json();
    }
    function showConnection(value: HubState) {
        if (connection) connection.textContent = value.instance_id ? (value.online ? '已连接到 Hub' : '已注册，正在连接 Hub…') : '尚未接入 Hub';
    }
    async function sync() {
        if (syncing || disposed) return;
        syncing = true;
        try {
            const response = await fetch(basePath + '-/hub-status', { headers: headers(), cache: 'no-store' });
            if (disposed) return;
            if (response.ok) {
                const value = await response.json();
                if (['red', 'gray', 'yellow', 'green'].includes(value.color)) window.dispatchEvent(new CustomEvent('gotty-hub-status', { detail: value.color }));
                await fetch(basePath + '-/hub-view', { method: 'POST', headers: headers(), body: JSON.stringify({ ID: view, Viewing: document.visibilityState === 'visible' && document.hasFocus() && !!document.activeElement?.closest('.xterm') }) });
            } else window.dispatchEvent(new CustomEvent('gotty-hub-status', { detail: null }));
            if (dialog?.open && !submitting) showConnection(await state());
        } catch { /* The next poll recovers transient network failures. */ }
        finally { syncing = false; }
    }
    const panelOpened = (event: Event) => { if ((event as CustomEvent<string>).detail !== 'hub') dialog?.close(); };
    window.addEventListener(PANEL_OPEN_EVENT, panelOpened);
    button.onclick = async () => {
        if (dialog?.open) return;
        window.dispatchEvent(new CustomEvent(PANEL_OPEN_EVENT, { detail: 'hub' }));
        const currentDialog = document.createElement('dialog'); dialog = currentDialog;
        currentDialog.id = 'gotty-hub-dialog'; currentDialog.setAttribute('aria-labelledby', 'gotty-hub-title');
        const title = document.createElement('h2'); title.id = 'gotty-hub-title'; title.textContent = '接入 GoTTY Hub'; currentDialog.append(title);
        const form = document.createElement('form'); currentDialog.append(form);
        function field(label: string, type: string, name: string) {
            const wrap = document.createElement('label'); wrap.textContent = label;
            const input = document.createElement('input'); input.type = type; input.name = name; input.required = true; input.setAttribute('autocomplete', name === 'Username' ? 'username' : 'url');
            wrap.append(input); form.append(wrap); return input;
        }
        const url = field('Hub 地址', 'url', 'URL'); url.placeholder = 'https://hub.example.com';
        const username = field('用户名', 'text', 'Username'); username.maxLength = 64;
        connection = document.createElement('p'); connection.textContent = '正在读取配置…'; form.append(connection);
        const status = document.createElement('p'); status.setAttribute('role', 'status'); status.setAttribute('aria-live', 'polite'); form.append(status);
        const register = document.createElement('a'); register.textContent = '前往 Hub Web 注册'; register.target = '_blank'; register.rel = 'noopener noreferrer'; register.hidden = true; form.append(register);
        const actions = document.createElement('div'); actions.className = 'hub-actions'; form.append(actions);
        const close = document.createElement('button'); close.type = 'button'; close.textContent = '关闭'; close.onclick = () => currentDialog.close(); actions.append(close);
        const submit = document.createElement('button'); submit.type = 'submit'; submit.textContent = '接入 Hub'; submit.disabled = true; actions.append(submit);
        form.onsubmit = async event => {
            event.preventDefault(); if (submitting) return;
            submitting = true; submit.disabled = true; register.hidden = true; status.setAttribute('role', 'status'); status.textContent = '正在接入…';
            requestController = new AbortController();
            try {
                const response = await fetch(basePath + '-/hub', { method: 'POST', headers: headers(), signal: requestController.signal, body: JSON.stringify({ URL: url.value.trim(), Username: username.value.trim() }) });
                const result = await response.json().catch(() => ({ message: `接入失败（HTTP ${response.status}）` }));
                if (!response.ok) {
                    if (result.error === 'user_not_found') { const address = new URL(url.value); if (['https:', 'http:'].includes(address.protocol)) { register.href = address.origin + '/?auth=register'; register.hidden = false; } }
                    throw new Error(result.message || '接入失败，请稍后重试。');
                }
                status.textContent = '接入成功。'; showConnection(await state()); void sync();
            } catch (error) {
                if (!(error instanceof DOMException && error.name === 'AbortError')) { status.setAttribute('role', 'alert'); status.textContent = error instanceof Error ? error.message : String(error); }
            } finally { submitting = false; submit.disabled = false; requestController = undefined; }
        };
        currentDialog.onclose = () => { requestController?.abort(); currentDialog.remove(); if (dialog === currentDialog) { dialog = undefined; connection = undefined; } button.focus(); };
        document.body.append(currentDialog); currentDialog.showModal();
        try { const value = await state(); if (dialog === currentDialog) { url.value = value.url || ''; username.value = value.username || ''; showConnection(value); } }
        catch (error) { status.setAttribute('role', 'alert'); status.textContent = error instanceof Error ? error.message : String(error); }
        finally { submit.disabled = false; }
    };
    const timer = setInterval(() => { void sync(); }, 3000); void sync();
    return () => {
        disposed = true; clearInterval(timer); requestController?.abort(); dialog?.close();
        window.removeEventListener(PANEL_OPEN_EVENT, panelOpened); button.remove(); style.remove();
        if (terminalState && previousTop !== undefined) terminalState.style.top = previousTop;
    };
}
