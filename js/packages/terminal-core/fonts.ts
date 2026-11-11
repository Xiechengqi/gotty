// Web fonts belong to the page; verified bytes, rather than installation flags,
// are persisted so a reload can register them without another network request.
import manifest from './font-manifest.json';
import {FONT_FILES} from './font-files';
import {FONT_FAMILIES, isFontAvailable} from './appearance';

export type FontState = 'downloadable' | 'loading' | 'cached' | 'ready' | 'system' | 'unavailable' | 'error';
export type LoadedFont = {fontFamily: string; cached: boolean};
// xterm caches cell metrics and its glyph atlas when an option is assigned.
// Reassigning the same family is ignored, even after its web font loads. Use
// public option setters to invalidate both caches before the scheduled refit.
export function applyLoadedFont(options: {fontFamily?: string}, family: string): void {
    if (options.fontFamily === family && family !== 'monospace') options.fontFamily = 'monospace';
    options.fontFamily = family;
}
const states = new Map<string, FontState>();
const pending = new Map<string, Promise<LoadedFont>>();
const listeners = new Set<() => void>();
const knownFiles = new Set(manifest.flatMap(font => font.files.map(file => file.file)));
let database: Promise<IDBDatabase | undefined> | undefined;

export function fontById(id: string) {return FONT_FAMILIES.find(font => font.id === id);}
export function fontIdForFamily(value?: string) {
    return FONT_FAMILIES.find(font => font.value === value || font.name === value)?.id;
}
export function fontState(id: string): FontState {
    const item = fontById(id);
    if (!item) return 'unavailable';
    if (!manifest.some(font => font.id === id)) return isFontAvailable(item.name) ? 'system' : 'unavailable';
    return states.get(id) || 'downloadable';
}
export function subscribeFonts(listener: () => void): () => void {
    listeners.add(listener); return () => {listeners.delete(listener);};
}
function publish(id: string, state: FontState) {
    states.set(id, state); for (const listener of listeners) listener();
}
function openCache(): Promise<IDBDatabase | undefined> {
    if (database) return database;
    database = new Promise(resolve => {
        let settled = false;
        const finish = (db?: IDBDatabase) => {if (settled) {db?.close(); return;} settled = true; clearTimeout(timer); resolve(db);};
        const timer = setTimeout(() => finish(), 1000);
        try {
            if (typeof indexedDB === 'undefined') {finish(); return;}
            const request = indexedDB.open('gotty-terminal-fonts', 1);
            request.onupgradeneeded = () => {request.result.createObjectStore('files');};
            request.onerror = request.onblocked = () => finish();
            request.onsuccess = () => {
                const db = request.result;
                db.onversionchange = () => {db.close(); database = undefined;};
                finish(db);
            };
        } catch {finish();}
    });
    return database;
}
async function cacheRequest<T>(mode: IDBTransactionMode, action: (store: IDBObjectStore) => IDBRequest<T>): Promise<T | undefined> {
    const db = await openCache(); if (!db) return undefined;
    return new Promise(resolve => {
        let done = false;
        const finish = (value?: T) => {if (!done) {done = true; clearTimeout(timer); resolve(value);}};
        const timer = setTimeout(() => finish(), 1000);
        try {
            const tx = db.transaction('files', mode), request = action(tx.objectStore('files'));
            tx.oncomplete = () => finish(request.result);
            tx.onerror = tx.onabort = () => finish();
        } catch {finish();}
    });
}
export async function inspectFontCache(): Promise<void> {
    const keys = await cacheRequest('readonly', store => store.getAllKeys());
    if (!keys) return;
    const present = new Set(keys.map(String));
    for (const key of keys) if (!knownFiles.has(String(key))) void cacheRequest('readwrite', store => store.delete(key));
    for (const font of manifest) {
        if (!states.has(font.id) && font.files.every(file => present.has(file.file))) publish(font.id, 'cached');
    }
}
async function download(file: string, size: number): Promise<ArrayBuffer> {
    const abort = new AbortController(), timer = setTimeout(() => abort.abort(), 15000);
    try {
        const response = await fetch(FONT_FILES[file], {credentials: 'same-origin', signal: abort.signal});
        if (!response.ok) throw Error(`Font download failed (${response.status})`);
        const data = await response.arrayBuffer();
        if (data.byteLength !== size) throw Error('Incomplete font download');
        return data;
    } finally {clearTimeout(timer);}
}
export function ensureFont(id: string): Promise<LoadedFont> {
    const item = fontById(id);
    if (!item) return Promise.reject(Error('Unknown font'));
    const source = manifest.find(font => font.id === id);
    if (!source) return fontState(id) === 'system'
        ? Promise.resolve({fontFamily: item.value, cached: true}) : Promise.reject(Error('System font is unavailable'));
    const existing = pending.get(id); if (existing) return existing;
    publish(id, 'loading');
    const work = (async () => {
        if (typeof FontFace === 'undefined' || !document.fonts) throw Error('Web fonts are unavailable');
        const files = await Promise.all(source.files.map(async file => {
            let data = await cacheRequest<ArrayBuffer>('readonly', store => store.get(file.file));
            let cached = data instanceof ArrayBuffer && data.byteLength === file.bytes;
            if (!cached) data = await download(file.file, file.bytes);
            let face: FontFace;
            try {face = await new FontFace(item.name, data!, {weight: file.weight, style: file.style}).load();}
            catch (error) {
                if (!cached) throw error;
                await cacheRequest('readwrite', store => store.delete(file.file));
                data = await download(file.file, file.bytes); cached = false;
                face = await new FontFace(item.name, data, {weight: file.weight, style: file.style}).load();
            }
            if (!cached) cached = (await cacheRequest('readwrite', store => store.put(data!, file.file))) !== undefined;
            return {face, cached};
        }));
        for (const file of files) document.fonts.add(file.face);
        const cached = files.every(file => file.cached);
        publish(id, cached ? 'cached' : 'ready');
        return {fontFamily: item.value, cached};
    })().catch(error => {pending.delete(id); publish(id, 'error'); throw error;});
    pending.set(id, work); return work;
}
