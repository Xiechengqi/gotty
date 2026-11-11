import {beforeEach, expect, it, vi} from 'vitest';
import {DEFAULT_FONT_FAMILY, DEFAULT_FONT_ID} from '../../packages/terminal-core/appearance';
import {ensureFont} from '../../packages/terminal-core/fonts';
import {applyTerminalFont, fontDiagnostics, type FontApplication} from '../../packages/terminal-core/font-rendering';

vi.mock('../../packages/terminal-core/fonts', async original => ({
    ...await original<typeof import('../../packages/terminal-core/fonts')>(), ensureFont: vi.fn(),
}));
beforeEach(() => {vi.mocked(ensureFont).mockReset();});
const deferred = <T>() => {let resolve!: (value: T) => void; let reject!: (reason?: unknown) => void;
    const promise = new Promise<T>((yes, no) => {resolve = yes; reject = no;}); return {promise, resolve, reject};};
const loaded = (fontFamily = DEFAULT_FONT_FAMILY) => ({fontFamily, cached: true});

it('keeps the applied font during download and invalidates metrics even for the same family', async () => {
    const wait = deferred<ReturnType<typeof loaded>>(); vi.mocked(ensureFont).mockReturnValue(wait.promise);
    const writes: string[] = []; let family = DEFAULT_FONT_FAMILY;
    const options = {get fontFamily() {return family;}, set fontFamily(value: string) {writes.push(value); family = value;}};
    const states: FontApplication[] = [], refit = vi.fn();
    const pending = applyTerminalFont({options, family, isCurrent: () => true, onChange: value => states.push(value), onApplied: refit});
    expect(writes).toEqual([]); expect(states[0]).toMatchObject({status: 'loading', appliedFamily: family});
    wait.resolve(loaded()); await pending;
    expect(writes).toEqual(['monospace', DEFAULT_FONT_FAMILY]); expect(refit).toHaveBeenCalledOnce();
    expect(states.at(-1)).toEqual({requestedFamily: family, appliedFamily: family, status: 'ready'});
});

it('reports a bundled fallback without changing the requested preference', async () => {
    vi.mocked(ensureFont).mockRejectedValueOnce(Error('missing')).mockResolvedValueOnce(loaded());
    const options = {fontFamily: 'monospace'}, requested = 'Monaco, monospace';
    const state = await applyTerminalFont({options, family: requested, isCurrent: () => true, onChange: () => {}, onApplied: () => {}});
    expect(ensureFont).toHaveBeenNthCalledWith(2, DEFAULT_FONT_ID);
    expect(state).toEqual({requestedFamily: requested, appliedFamily: DEFAULT_FONT_FAMILY, status: 'fallback'});
    const host = document.createElement('div'); fontDiagnostics(host, state!);
    expect(host.dataset.fontRequested).toBe(requested); expect(host.dataset.fontApplied).toBe(DEFAULT_FONT_FAMILY);
});

it('uses monospace when web fonts are unavailable, without retrying a failed default', async () => {
    vi.mocked(ensureFont).mockRejectedValue(Error('offline'));
    const options = {fontFamily: 'monospace'};
    const state = await applyTerminalFont({options, family: DEFAULT_FONT_FAMILY, isCurrent: () => true, onChange: () => {}, onApplied: () => {}});
    expect(state?.status).toBe('fallback'); expect(options.fontFamily).toBe('monospace'); expect(ensureFont).toHaveBeenCalledOnce();
});

it.each(['resolve', 'reject'] as const)('ignores a stale or disposed font request on %s', async result => {
    const wait = deferred<ReturnType<typeof loaded>>(); vi.mocked(ensureFont).mockReturnValue(wait.promise);
    let current = true; const onChange = vi.fn(), onApplied = vi.fn(), options = {fontFamily: 'monospace'};
    const pending = applyTerminalFont({options, family: "'Fira Code', monospace", isCurrent: () => current, onChange, onApplied});
    current = false;
    if (result === 'resolve') wait.resolve(loaded("'Fira Code', monospace")); else wait.reject(Error('offline'));
    expect(await pending).toBeUndefined(); expect(options.fontFamily).toBe('monospace');
    expect(onChange).toHaveBeenCalledOnce(); expect(onApplied).not.toHaveBeenCalled(); expect(ensureFont).toHaveBeenCalledOnce();
});
