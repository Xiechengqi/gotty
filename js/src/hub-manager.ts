// Local management UI. Device credentials stay in the GoTTY backend.
declare var gotty_hub_enabled: boolean;
type HubState = {url: string; username: string; name: string; instance_id: string; online: boolean; paused?: boolean; superseded?: boolean; notice?: string};
const PANEL_OPEN_EVENT = 'gotty-panel-open';

export function initHubManager(basePath: string, authToken: string): () => void {
    if (typeof gotty_hub_enabled === 'undefined' || !gotty_hub_enabled) return () => {};
    const style = document.createElement('style');
    style.textContent = `
#gotty-hub-btn {position:fixed;top:170px;right:10px;width:28px;height:28px;border-radius:4px;border:none;background:rgba(0,0,0,.7);color:#fff;padding:0;cursor:pointer;z-index:1000;display:flex;align-items:center;justify-content:center;line-height:1}
#gotty-hub-btn:hover {background:rgba(0,0,0,.75)}
#gotty-hub-btn:focus-visible {outline:2px solid #9aa0a6;outline-offset:2px}
#gotty-hub-panel {position:fixed;top:170px;right:46px;z-index:9999;color:#eee;background:rgba(30,30,30,.94);backdrop-filter:blur(8px);border:1px solid rgba(255,255,255,.12);border-radius:10px;width:340px;max-width:calc(100vw - 62px);max-height:calc(100dvh - 16px);overflow:auto;padding:16px;box-sizing:border-box;box-shadow:0 8px 24px rgba(0,0,0,.5);font:13px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}
#gotty-hub-panel[hidden] {display:none}
#gotty-hub-panel .hub-heading {display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:12px}
#gotty-hub-panel h2 {font-size:15px;margin:0}
#gotty-hub-panel form {display:flex;flex-direction:column;gap:12px}
#gotty-hub-panel label {display:flex;flex-direction:column;gap:6px}
#gotty-hub-panel input {width:100%;min-width:0;box-sizing:border-box;background:#333;border:1px solid #555;border-radius:4px;padding:8px 10px;color:inherit;font:inherit}
#gotty-hub-panel input:read-only {background:rgba(255,255,255,.03);color:#bbb;border-color:rgba(255,255,255,.1)}
#gotty-hub-panel input:focus-visible {outline:2px solid #9aa0a6;outline-offset:1px}
#gotty-hub-panel p {margin:0;overflow-wrap:anywhere}
#gotty-hub-panel .hub-actions {display:flex;justify-content:flex-end;gap:8px;flex-wrap:wrap}
#gotty-hub-panel button {font:inherit;background:rgba(255,255,255,.06);color:inherit;border:1px solid rgba(255,255,255,.15);border-radius:4px;padding:6px 10px;cursor:pointer}
#gotty-hub-panel button:hover {background:rgba(255,255,255,.12)}
#gotty-hub-panel button:disabled {opacity:.5;cursor:default}
#gotty-hub-panel a {color:#8ab4f8}
#gotty-hub-panel .hub-result:empty {display:none}
#gotty-hub-panel .hub-result {padding:10px 12px;border-radius:6px;background:rgba(255,255,255,.06)}
#gotty-hub-panel .hub-result[data-tone=success] {background:#166534;color:#f0fdf4;border:1px solid #22c55e;font-weight:600}
#gotty-hub-panel .hub-result[role=alert] {background:rgba(127,29,29,.65);color:#fecaca;border:1px solid #b91c1c}
`;
    document.head.append(style);
    const button = document.createElement('button');
    button.id = 'gotty-hub-btn'; button.type = 'button'; button.title = '接入 GoTTY Hub';
    button.setAttribute('aria-label', button.title); button.setAttribute('aria-expanded', 'false'); button.setAttribute('aria-controls', 'gotty-hub-panel');
    button.innerHTML = '<svg width="14" height="14" viewBox="0 0 16 16" fill="none" aria-hidden="true"><path d="M6 5.5H4.5a2.5 2.5 0 000 5H6m4-5h1.5a2.5 2.5 0 010 5H10m-5-2h6" stroke="rgba(255,255,255,.7)" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round"/></svg>';
    document.body.append(button);
    const panel = document.createElement('div'); panel.id = 'gotty-hub-panel'; panel.hidden = true;
    panel.setAttribute('role', 'dialog'); panel.setAttribute('aria-labelledby', 'gotty-hub-title');
    const heading = document.createElement('div'); heading.className = 'hub-heading'; panel.append(heading);
    const title = document.createElement('h2'); title.id = 'gotty-hub-title'; title.textContent = '接入 GoTTY Hub'; heading.append(title);
    const close = document.createElement('button'); close.type = 'button'; close.textContent = '×'; close.setAttribute('aria-label', '关闭 Hub 设置'); heading.append(close);
    const form = document.createElement('form'); panel.append(form);
    function field(label: string, name: string, type: string, required: boolean) {
        const wrap = document.createElement('label'); wrap.textContent = label;
        const input = document.createElement('input'); input.name = name; input.type = type; input.required = required;
        input.autocomplete = name === 'Username' ? 'username' : 'off'; wrap.append(input); form.append(wrap); return input;
    }
    const url = field('Hub 地址', 'URL', 'url', true); url.placeholder = 'http://hub.example.com:8088';
    const username = field('用户名', 'Username', 'text', true); username.maxLength = 64;
    const name = field('Hub 中显示的名字（可选）', 'Name', 'text', false); name.maxLength = 80; name.placeholder = '留空自动生成';
    const connection = document.createElement('p'); connection.textContent = '正在读取配置…'; form.append(connection);
    const status = document.createElement('p'); status.className = 'hub-result'; status.setAttribute('role', 'status'); status.setAttribute('aria-live', 'polite'); form.append(status);
    const register = document.createElement('a'); register.textContent = '前往 Hub Web 注册'; register.target = '_blank'; register.rel = 'noopener noreferrer'; register.hidden = true; form.append(register);
    const actions = document.createElement('div'); actions.className = 'hub-actions'; form.append(actions);
    function action(text: string) {const item = document.createElement('button'); item.type = 'button'; item.textContent = text; actions.append(item); return item;}
    const cancel = action('取消编辑'); cancel.hidden = true;
    const disconnect = action('断开'); disconnect.hidden = true;
    const reconnect = action('重新接入'); reconnect.hidden = true;
    const submit = action('接入 Hub'); submit.type = 'submit'; submit.disabled = true;
    document.body.append(panel);
    const terminalState = document.querySelector<HTMLElement>('.terminal-state'); const previousTop = terminalState?.style.top;
    if (terminalState) terminalState.style.top = '202px';
    const headers = () => ({'Content-Type': 'application/json', ...(authToken ? {Authorization: `Bearer ${authToken}`} : {})});
    const view = Array.from(crypto.getRandomValues(new Uint8Array(32)), b => b.toString(16).padStart(2, '0')).join('');
    let saved: HubState | undefined; let editing = false; let submitting = false; let syncing = false; let disposed = false; let revision = 0;
    let requestController: AbortController | undefined;
    const position = () => {if (!panel.hidden) panel.style.top = `${Math.max(8, Math.min(button.getBoundingClientRect().top, window.innerHeight - panel.offsetHeight - 8))}px`;};
    function hide(focus = false) {panel.hidden = true; button.setAttribute('aria-expanded', 'false'); if (focus) button.focus();}
    function result(text: string, tone = '') {status.textContent = text; status.dataset.tone = tone; status.setAttribute('role', tone === 'error' ? 'alert' : 'status'); position();}
    function render(value: HubState, restore = false) {
        saved = value; const bound = !!value.instance_id;
        if (restore) {url.value = value.url || ''; username.value = value.username || ''; name.value = value.name || '';}
        const readonly = bound && !editing;
        for (const field of [url, username, name]) {field.readOnly = readonly; field.disabled = submitting;}
        submit.textContent = readonly ? '编辑' : '接入 Hub'; submit.type = readonly ? 'button' : 'submit'; submit.disabled = submitting;
        reconnect.hidden = !bound; disconnect.hidden = !bound; cancel.hidden = !bound || !editing;
        reconnect.disabled = submitting || editing; disconnect.disabled = submitting || editing || !!value.paused; cancel.disabled = submitting;
        connection.textContent = value.superseded ? '此身份已被另一个 GoTTY 使用，已暂停重连。请检查是否共用了身份文件。' : value.notice || (bound ? (value.paused ? '已断开 Hub，配置已保留' : value.online ? '已连接到 Hub' : '已注册，正在连接 Hub…') : '尚未接入 Hub');
        position();
    }
    async function state(): Promise<HubState> {
        const response = await fetch(basePath + '-/hub', {headers: headers(), cache: 'no-store'});
        if (!response.ok) throw Error('无法读取 Hub 配置，请检查 GoTTY 登录状态。');
        return response.json();
    }
    async function operate(action: string) {
        if (submitting) return; submitting = true; revision++; register.hidden = true;
        const config = action === 'disconnect' ? {Action: action} : {Action: action, URL: url.value.trim(), Username: username.value.trim(), Name: name.value.trim()};
        if (saved) render(saved); else submit.disabled = true;
        result(action === 'disconnect' ? '正在断开…' : '正在接入…'); requestController = new AbortController();
        try {
            const response = await fetch(basePath + '-/hub', {method: 'POST', headers: headers(), signal: requestController.signal, body: JSON.stringify(config)});
            const value = await response.json().catch(() => ({message: `操作失败（HTTP ${response.status}）`}));
            if (!response.ok) {
                if (value.error === 'user_not_found') {try {const address = new URL(url.value); if (['https:', 'http:'].includes(address.protocol)) {register.href = address.origin + '/?auth=register'; register.hidden = false;}} catch { /* Invalid URL is reported by the server. */ }}
                throw Error(value.message || '操作失败，请稍后重试。');
            }
            if (disposed) return;
            editing = false; render(value, true);
            result(action === 'disconnect' ? `已断开 ${value.url}，配置已保留` : `接入 ${value.url} 成功`, action === 'disconnect' ? '' : 'success');
            void sync();
        } catch (error) {if (!disposed && !(error instanceof DOMException && error.name === 'AbortError')) result(error instanceof Error ? error.message : String(error), 'error');}
        finally {submitting = false; requestController = undefined; if (!disposed) {if (saved) render(saved); else submit.disabled = false;}}
    }
    form.onsubmit = event => {event.preventDefault(); if (form.reportValidity()) void operate('connect');};
    submit.onclick = event => {if (saved?.instance_id && !editing) {event.preventDefault(); editing = true; render(saved); result(''); name.focus();}};
    cancel.onclick = () => {editing = false; if (saved) render(saved, true); result('');};
    reconnect.onclick = () => {void operate('reconnect');}; disconnect.onclick = () => {void operate('disconnect');}; close.onclick = () => hide(true);
    button.onclick = async () => {
        if (!panel.hidden) {hide(); return;}
        window.dispatchEvent(new CustomEvent(PANEL_OPEN_EVENT, {detail: 'hub'})); panel.hidden = false; button.setAttribute('aria-expanded', 'true'); position();
        const current = revision;
        try {const value = await state(); if (!disposed && current === revision && !submitting) {render(value, !editing); if (!value.instance_id) url.focus();}}
        catch (error) {if (!disposed) {result(error instanceof Error ? error.message : String(error), 'error'); submit.disabled = false;}}
    };
    const panelOpened = (event: Event) => {if ((event as CustomEvent<string>).detail !== 'hub') hide();};
    const outside = (event: Event) => {const target = event.target as Node; if (!panel.contains(target) && !button.contains(target)) hide();};
    const keydown = (event: KeyboardEvent) => {if (event.key === 'Escape' && !panel.hidden) {event.preventDefault(); hide(true);}};
    async function sync() {
        if (syncing || disposed) return; syncing = true; const current = revision;
        try {
            const response = await fetch(basePath + '-/hub-status', {headers: headers(), cache: 'no-store'});
            if (disposed) return;
            if (response.ok) {
                const value = await response.json();
                if (['red', 'gray', 'yellow', 'green'].includes(value.color)) window.dispatchEvent(new CustomEvent('gotty-hub-status', {detail: value.color}));
                await fetch(basePath + '-/hub-view', {method: 'POST', headers: headers(), body: JSON.stringify({ID: view, Viewing: document.visibilityState === 'visible' && document.hasFocus() && !!document.activeElement?.closest('.xterm')})});
            } else window.dispatchEvent(new CustomEvent('gotty-hub-status', {detail: null}));
            if (!panel.hidden && !submitting) {const value = await state(); if (!disposed && current === revision && !submitting) render(value, false);}
        } catch { /* Next poll recovers transient network failures. */ }
        finally {syncing = false;}
    }
    window.addEventListener(PANEL_OPEN_EVENT, panelOpened); document.addEventListener('pointerdown', outside); document.addEventListener('keydown', keydown); window.addEventListener('resize', position);
    const timer = setInterval(() => {void sync();}, 3000); void sync();
    return () => {
        disposed = true; clearInterval(timer); requestController?.abort();
        window.removeEventListener(PANEL_OPEN_EVENT, panelOpened); document.removeEventListener('pointerdown', outside); document.removeEventListener('keydown', keydown); window.removeEventListener('resize', position);
        panel.remove(); button.remove(); style.remove(); if (terminalState && previousTop !== undefined) terminalState.style.top = previousTop;
    };
}
