import { ConnectionFactory } from "./websocket";
import { WebTTY, protocols } from "./webtty";
import { OurXterm } from "./xterm";
import { createIdleAlert } from "./idle-alert";
import { installFaviconAlert } from "./favicon-alert";

// @TODO remove these
declare var gotty_auth_token: string;
declare var gotty_term: string;
declare var gotty_ws_query_args: string;
declare var gotty_enable_idle_alert: boolean;
declare var gotty_idle_alert_timeout: number;

const elem = document.getElementById("terminal")

if (elem !== null) {
    var term: OurXterm;
    term = new OurXterm(elem);

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

    const faviconStopTimeoutMs = 3000;
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
    const url = (httpsEnabled ? 'wss://' : 'ws://') + window.location.host + window.location.pathname + 'ws' + queryArgs;
    const args = window.location.search;
    const factory = new ConnectionFactory(url, protocols);
    const wt = new WebTTY(term, factory, args, gotty_auth_token);
    const closer = wt.open();

    // According to https://developer.mozilla.org/en-US/docs/Web/API/Window/unload_event
    // this event is unreliable and in some cases (Firefox is mentioned), having an
    // "unload" event handler can have unwanted side effects. Consider commenting it out.
    window.addEventListener("unload", () => {
        uninstallFaviconAlert();
        closer();
        term.close();
    });
};
