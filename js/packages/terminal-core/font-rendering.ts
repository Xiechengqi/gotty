import {DEFAULT_FONT_ID, isFontAvailable} from './appearance';
import {applyLoadedFont, ensureFont, fontIdForFamily} from './fonts';

export type FontApplication = {
    requestedFamily: string;
    appliedFamily: string;
    status: 'loading' | 'ready' | 'fallback';
};

// Start a new renderer with monospace. Keep its current, loaded font during a
// download, then invalidate both metrics and the glyph atlas before refitting.
// The caller's generation guard prevents late downloads changing a newer choice
// or a disposed terminal. A fallback never overwrites the saved preference.
export async function applyTerminalFont({options, family, isCurrent, onChange, onApplied}: {
    options: {fontFamily?: string}; family: string; isCurrent: () => boolean;
    onChange: (state: FontApplication) => void; onApplied: () => void;
}): Promise<FontApplication | undefined> {
    if (!isCurrent()) return;
    onChange({requestedFamily: family, appliedFamily: options.fontFamily || 'monospace', status: 'loading'});
    let appliedFamily: string;
    let status: FontApplication['status'] = 'ready';
    const id = fontIdForFamily(family);
    try {
        if (id) appliedFamily = (await ensureFont(id)).fontFamily;
        else {
            // Preserve older custom system-font preferences when available.
            const first = family.split(',')[0].trim().replace(/^['"]|['"]$/g, '');
            if (!isFontAvailable(first)) throw Error('System font is unavailable');
            appliedFamily = family;
        }
    } catch {
        if (!isCurrent()) return;
        status = 'fallback';
        try {
            appliedFamily = id === DEFAULT_FONT_ID ? 'monospace' : (await ensureFont(DEFAULT_FONT_ID)).fontFamily;
        } catch {appliedFamily = 'monospace';}
    }
    if (!isCurrent()) return;
    applyLoadedFont(options, appliedFamily);
    const state = {requestedFamily: family, appliedFamily, status};
    onChange(state);
    onApplied();
    return state;
}

// These describe the primary terminal font, not OS-dependent CJK/emoji glyph
// fallback. They are useful when comparing live clients without private xterm APIs.
export function fontDiagnostics(element: HTMLElement | undefined | null, state: FontApplication): void {
    if (!element) return;
    element.dataset.fontRequested = state.requestedFamily;
    element.dataset.fontApplied = state.appliedFamily;
    element.dataset.fontStatus = state.status;
}
