import {afterEach, beforeEach, expect, it, vi} from 'vitest';
import {InputSender, bindPaste, type PasteState} from '../../packages/terminal-core/paste';

let senders: InputSender[] = [];
beforeEach(() => vi.useFakeTimers());
afterEach(() => {senders.forEach(sender => sender.dispose()); senders = []; vi.useRealTimers();});
function fixture(confirmed = true) {
    const frames: string[] = [], states: (PasteState | undefined)[] = [], messages: string[] = [];
    let buffered = 0, open = true;
    const sender = new InputSender({send: frame => {if (!open) return false; frames.push(frame); return true;}, buffered: () => buffered, chunkSize: () => 12285, changed: state => states.push(state), message: text => messages.push(text)});
    sender.acknowledged = confirmed; senders.push(sender);
    const chunks = () => frames.filter(frame => frame[0] === '5').map(frame => JSON.parse(frame.slice(1))).filter(chunk => !chunk.cancel);
    return {sender, frames, states, messages, chunks, buffered: (value: number) => {buffered = value;}, close: () => {open = false;}, state: () => states.at(-1)};
}

it('sends a 1 MiB Uint8Array without stack spread and yields under socket pressure', () => {
    const f = fixture(); const bytes = new Uint8Array(1024*1024).fill(65);
    f.buffered(65536); f.sender.input(bytes); expect(f.frames).toHaveLength(0);
    f.buffered(0); vi.advanceTimersByTime(8); expect(f.frames.length).toBe(4);
    vi.advanceTimersByTime(1000);
    expect(f.frames.reduce((size, frame) => size + atob(frame.slice(1)).length, 0)).toBe(bytes.length);
    expect(f.frames.every(frame => frame.length < 16384)).toBe(true);
});

it('bounds unacknowledged chunks and counts actual UTF-8 payload receipts', () => {
    const f = fixture(), text = '中文🙂\r\n'.repeat(10000), expected = new TextEncoder().encode(text.replace(/\r?\n/g, '\r'));
    f.sender.paste(text, true); expect(f.state()?.visible).toBe(true);
    vi.advanceTimersByTime(1000); expect(f.chunks()).toHaveLength(8); expect(f.state()?.loaded).toBe(0);
    expect(f.state()?.total).toBe(expected.length);
    let seen = 0, received = 0;
    const payload: number[] = [];
    while (f.state()?.status === 'running') {
        for (const chunk of f.chunks().slice(seen)) {
            const bytes = Uint8Array.from(atob(chunk.data), c => c.charCodeAt(0));
            expect(() => new TextDecoder('utf-8', {fatal: true}).decode(bytes)).not.toThrow();
            payload.push(...bytes); received += bytes.length; seen++;
            f.sender.reply({id: chunk.id, seq: chunk.seq, received, status: chunk.final ? 'done' : 'writing'});
        }
        vi.advanceTimersByTime(16);
    }
    expect(payload).toEqual(Array.from(expected));
    expect(f.state()).toMatchObject({status: 'done', loaded: expected.length, confirmed: true});
    expect(f.frames.every(frame => frame[0] === '5')).toBe(true);
});

it('keeps quick pastes quiet and reveals a slow small paste after 150 ms', () => {
    const fast = fixture(); fast.sender.paste('abc', false); vi.advanceTimersByTime(8);
    const chunk = fast.chunks()[0]; fast.sender.reply({id: chunk.id, seq: 0, received: 3, status: 'done'});
    expect(fast.states.every(state => !state?.visible)).toBe(true);
    const slow = fixture(); slow.sender.paste('🙂', false); vi.advanceTimersByTime(160);
    expect(slow.state()).toMatchObject({visible: true, total: 4, loaded: 0});
});

it('queues ordinary keys, allows protocol responses and drops queued keys on stop', () => {
    const f = fixture(); f.sender.paste('x'.repeat(300000), true); vi.advanceTimersByTime(16);
    f.sender.input('\r'); f.sender.input('\x1b[?1;2c');
    expect(f.frames.filter(frame => frame[0] === '1').map(frame => atob(frame.slice(1)))).toEqual(['\x1b[?1;2c']);
    f.sender.cancel(); expect(f.state()?.status).toBe('stopping');
    expect(JSON.parse(f.frames.at(-1)!.slice(1))).toMatchObject({cancel: true});
    const sent = f.chunks().length;
    f.sender.reply({id: f.chunks()[0].id, seq: 0, received: 3, status: 'cancelled'});
    vi.advanceTimersByTime(1000);
    expect(f.state()).toMatchObject({status: 'cancelled', loaded: 3});
    expect(f.chunks()).toHaveLength(sent);
    expect(f.frames.some(frame => frame[0] === '1' && atob(frame.slice(1)) === '\r')).toBe(false);
});

it('never replays a paste or queued commands after disconnect/send failure', () => {
    const f = fixture(); f.sender.paste('x'.repeat(1024*1024), true); vi.advanceTimersByTime(16); f.sender.input('Enter later\r');
    f.sender.disconnect(); const count = f.frames.length;
    f.sender.acknowledged = true; vi.advanceTimersByTime(60000);
    expect(f.frames).toHaveLength(count); expect(f.state()?.status).toBe('error');
    const failure = fixture(); failure.close(); failure.sender.paste('test', false); vi.advanceTimersByTime(8);
    expect(failure.state()?.status).toBe('error'); expect(failure.frames).toHaveLength(0);
});

it('timeouts and invalid confirmations stop sending without claiming success', () => {
    const f = fixture(); f.sender.paste('x'.repeat(300000), true); vi.advanceTimersByTime(30008);
    expect(f.state()).toMatchObject({status: 'error', loaded: 0});
    expect(JSON.parse(f.frames.at(-1)!.slice(1)).cancel).toBe(true);
    const invalid = fixture(); invalid.sender.paste('abc', false); vi.advanceTimersByTime(8);
    invalid.sender.reply({id: invalid.chunks()[0].id, seq: 4, received: 3, status: 'done'});
    expect(invalid.state()).toMatchObject({status: 'error', loaded: 0});
});

it('legacy pastes normalize newlines and use one bracket pair with unconfirmed status', () => {
    const f = fixture(false), text = 'line🙂\n'.repeat(1000);
    f.sender.paste(text, true); vi.advanceTimersByTime(1000);
    const wire = f.frames.map(frame => atob(frame.slice(1))).join('');
    const decoded = new TextDecoder().decode(Uint8Array.from(wire, c => c.charCodeAt(0)));
    expect(decoded).toBe('\x1b[200~' + text.replace(/\n/g, '\r') + '\x1b[201~');
    expect(f.state()).toMatchObject({status: 'sent', confirmed: false});
});

it('captures native paste once ahead of xterm bubbling and respects disabled input', () => {
    const root = document.createElement('div'), textarea = document.createElement('textarea'); root.append(textarea);
    const send = vi.fn(), bubble = vi.fn(); let enabled = true;
    const unbind = bindPaste(root, () => ({enabled, bracketed: true}), send); textarea.addEventListener('paste', bubble);
    const paste = () => {const event = new Event('paste', {bubbles: true, cancelable: true}); Object.defineProperty(event, 'clipboardData', {value: {getData: () => 'external'}}); textarea.dispatchEvent(event); return event;};
    expect(paste().defaultPrevented).toBe(true); expect(send).toHaveBeenCalledExactlyOnceWith('external', true); expect(bubble).not.toHaveBeenCalled();
    enabled = false; paste(); expect(send).toHaveBeenCalledTimes(1);
    unbind(); paste(); expect(bubble).toHaveBeenCalledTimes(1);
});
