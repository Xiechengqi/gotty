import {BUILTIN_THEMES} from "./themes";
export {BUILTIN_THEMES} from "./themes";
// Display defaults and preference translation shared by GoTTY and GoTTY Hub.
// The complete theme catalogue is supplied by GoTTY, alongside its preferences.
export const DEFAULT_THEME = "nord";
export const DEFAULT_FONT_FAMILY = "'Source Code Pro', monospace";
export const FONT_SIZES = [10, 12, 14, 16, 18, 20, 24];
export const FONT_FAMILIES = [
    { id: "dejavu-sans-mono", name: "DejaVu Sans Mono", value: "'DejaVu Sans Mono', monospace" },
    { id: "jetbrains-mono", name: "JetBrains Mono", value: "'JetBrains Mono', monospace" },
    { id: "fira-code", name: "Fira Code", value: "'Fira Code', monospace" },
    { id: "source-code-pro", name: "Source Code Pro", value: DEFAULT_FONT_FAMILY },
    { id: "monaco", name: "Monaco", value: "Monaco, monospace" },
    { id: "menlo", name: "Menlo", value: "Menlo, monospace" },
    { id: "cascadia-code", name: "Cascadia Code", value: "'Cascadia Code', monospace" },
    { id: "monospace", name: "monospace", value: "monospace" },
];

export type ThemeMap = Record<string, Record<string, string>>;
export type Appearance = { theme?: string; fontSize?: number; fontFamily?: string };
export interface DisplayOptions {
    fontSize: number;
    fontFamily: string;
    scrollback: number;
    cursorBlink: boolean;
    cursorStyle?: "block" | "underline" | "bar";
    theme: Record<string, string>;
    customGlyphs: boolean;
    rescaleOverlappingGlyphs: boolean;
}

// Also used before the remote configuration arrives and with older Connectors.
export const NORD_THEME = xtermTheme(BUILTIN_THEMES.nord);

export function defaultDisplayOptions(): DisplayOptions {
    return { fontSize: 14, fontFamily: DEFAULT_FONT_FAMILY, scrollback: 500, cursorBlink: false,
        customGlyphs: true, rescaleOverlappingGlyphs: true, theme: { ...NORD_THEME } };
}

export function xtermTheme(colors: Record<string, string>): Record<string, string> {
    return { ...colors, ...(colors.selection ? { selectionBackground: colors.selection } : {}) };
}

export function terminalPreferences(value: Record<string, unknown>): Partial<DisplayOptions> {
    const out: Partial<DisplayOptions> = {};
    if (typeof value["font-family"] === "string") out.fontFamily = value["font-family"];
    const size = value["font-size"];
    if (typeof size === "number" && Number.isFinite(size) && size > 0) out.fontSize = size;
    if (typeof value["cursor-blink"] === "boolean") out.cursorBlink = value["cursor-blink"];
    const style = value["cursor-style"];
    if (style === "block" || style === "bar" || style === "underline") out.cursorStyle = style;
    const scrollback = value["scrollback-lines"];
    if (typeof scrollback === "number" && Number.isInteger(scrollback) && scrollback >= 0) out.scrollback = scrollback;
    if (value.theme && typeof value.theme === "object" && !Array.isArray(value.theme)) {
        const colors = Object.fromEntries(Object.entries(value.theme).filter(([, color]) => typeof color === "string"));
        out.theme = xtermTheme(colors as Record<string, string>);
    }
    return out;
}

export function displayOptions(preferences: Record<string, unknown>, themes: ThemeMap, appearance: Appearance): DisplayOptions {
    const out = { ...defaultDisplayOptions(), ...terminalPreferences(preferences) };
    // Match GoTTY's picker: the saved theme wins, otherwise use Nord.
    out.theme = xtermTheme(themes[appearance.theme || DEFAULT_THEME] || themes[DEFAULT_THEME] || NORD_THEME);
    out.fontFamily = appearance.fontFamily || DEFAULT_FONT_FAMILY;
    if (appearance.fontSize && appearance.fontSize >= 8 && appearance.fontSize <= 48) out.fontSize = appearance.fontSize;
    return out;
}

export function themeLabel(name: string): string {
    const labels: Record<string, string> = { default: "Default", nord: "Nord", dracula: "Dracula",
        "solarized-dark": "Solarized Dark", monokai: "Monokai", light: "Light" };
    return labels[name] || name;
}

// document.fonts.check() also returns true for missing fonts; compare fallbacks.
export function isFontAvailable(name: string): boolean {
    if (["monospace", "serif", "sans-serif", "cursive", "fantasy", "system-ui"].includes(name.toLowerCase())) return true;
    const ctx = document.createElement("canvas").getContext("2d");
    if (!ctx) return true;
    ctx.font = `72px '${name}', serif`;
    const serif = ctx.measureText("mmmmmmmmmmlliWM@#").width;
    ctx.font = `72px '${name}', monospace`;
    return Math.abs(serif - ctx.measureText("mmmmmmmmmmlliWM@#").width) < 0.5;
}
