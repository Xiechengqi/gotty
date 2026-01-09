export interface FaviconAlertOptions {
    stopTimeoutMs: number;
    onActivity: (cb: () => void) => () => void;
}

type FaviconColor = 'red' | 'gray' | 'yellow' | 'green';

const COLORS: Record<FaviconColor, string> = {
    red: '#de3c28',
    gray: '#9aa0a6',
    yellow: '#fbbc04',
    green: '#34a853',
};

const LINK_ID_SVG = 'gotty-dynamic-favicon-svg';
const LINK_ID_PNG = 'gotty-dynamic-favicon-png';

function ensureFaviconLink(id: string, type: string): HTMLLinkElement {
    const existing = document.getElementById(id);
    if (existing && existing instanceof HTMLLinkElement) return existing;

    const link = document.createElement('link');
    link.id = id;
    link.rel = 'icon';
    link.type = type;
    link.setAttribute('sizes', '32x32');

    document.head.appendChild(link);
    return link;
}

function createSvgDataUrl(color: string): string {
    const svg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><circle cx="32" cy="32" r="26" fill="${color}"/></svg>`;
    return `data:image/svg+xml,${encodeURIComponent(svg)}`;
}

function createPngDataUrl(color: string): string {
    const canvas = document.createElement('canvas');
    const size = 64;
    canvas.width = size;
    canvas.height = size;

    const ctx = canvas.getContext('2d');
    if (!ctx) return '';

    ctx.clearRect(0, 0, size, size);

    const radius = 26;
    const center = size / 2;

    ctx.beginPath();
    ctx.arc(center, center, radius, 0, 2 * Math.PI);
    ctx.fillStyle = color;
    ctx.fill();

    return canvas.toDataURL('image/png');
}

export function installFaviconAlert(options: FaviconAlertOptions): () => void {
    const stopTimeoutMs = Math.max(1, Math.floor(options.stopTimeoutMs));
    const faviconSvgLink = ensureFaviconLink(LINK_ID_SVG, 'image/svg+xml');
    const faviconPngLink = ensureFaviconLink(LINK_ID_PNG, 'image/png');

    faviconSvgLink.setAttribute('sizes', 'any');

    const svgUrlCache: Partial<Record<FaviconColor, string>> = {};
    const pngUrlCache: Partial<Record<FaviconColor, string>> = {};
    let lastColor: FaviconColor | null = null;

    let hasFocus = document.hasFocus();
    let isViewing = document.visibilityState === 'visible' && hasFocus;

    let isActive = false;
    let needsAttention = false;
    let stopTimerId: number | null = null;
    let stopDeadlineMs: number | null = null;

    const setColor = (color: FaviconColor) => {
        if (lastColor === color) return;
        lastColor = color;

        const cachedSvg = svgUrlCache[color];
        if (cachedSvg) {
            faviconSvgLink.href = cachedSvg;
        } else {
            const svgUrl = createSvgDataUrl(COLORS[color]);
            svgUrlCache[color] = svgUrl;
            faviconSvgLink.href = svgUrl;
        }

        const cachedPng = pngUrlCache[color];
        if (cachedPng) {
            faviconPngLink.href = cachedPng;
            return;
        }

        const pngUrl = createPngDataUrl(COLORS[color]);
        if (pngUrl) {
            pngUrlCache[color] = pngUrl;
            faviconPngLink.href = pngUrl;
        }
    };

    const render = () => {
        if (isActive) {
            setColor('yellow');
            return;
        }
        if (isViewing) {
            setColor('red');
            return;
        }
        setColor(needsAttention ? 'green' : 'gray');
    };

    const checkStopped = () => {
        stopTimerId = null;
        if (stopDeadlineMs === null) return;

        const remainingMs = stopDeadlineMs - Date.now();
        if (remainingMs > 0) {
            stopTimerId = window.setTimeout(checkStopped, remainingMs);
            return;
        }

        stopDeadlineMs = null;
        if (!isActive) return;

        isActive = false;
        if (!isViewing) {
            needsAttention = true;
        }
        render();
    };

    const handleActivity = () => {
        stopDeadlineMs = Date.now() + stopTimeoutMs;

        if (!isActive) {
            isActive = true;
            render();
        }

        if (stopTimerId === null) {
            stopTimerId = window.setTimeout(checkStopped, stopTimeoutMs);
        }
    };

    const updateViewing = () => {
        const nextIsViewing = document.visibilityState === 'visible' && hasFocus;
        if (nextIsViewing === isViewing) return;

        isViewing = nextIsViewing;
        if (isViewing) {
            needsAttention = false;
        }
        render();
    };

    const onVisibilityChange = () => updateViewing();
    const onFocus = () => {
        hasFocus = true;
        updateViewing();
    };
    const onBlur = () => {
        hasFocus = false;
        updateViewing();
    };

    document.addEventListener('visibilitychange', onVisibilityChange);
    window.addEventListener('focus', onFocus);
    window.addEventListener('blur', onBlur);

    const unsubscribe = options.onActivity(handleActivity);

    render();

    return () => {
        document.removeEventListener('visibilitychange', onVisibilityChange);
        window.removeEventListener('focus', onFocus);
        window.removeEventListener('blur', onBlur);
        unsubscribe();
        if (stopTimerId !== null) {
            window.clearTimeout(stopTimerId);
            stopTimerId = null;
        }
        stopDeadlineMs = null;
    };
}
