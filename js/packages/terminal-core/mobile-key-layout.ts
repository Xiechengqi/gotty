import type {Modifiers, SpecialKey} from "./key-encoder";

export type KeyDef =
    | { name: string; label: string; kind: "mod"; mod: keyof Modifiers }
    | { name: string; label: string; kind: "char" }
    | { name: SpecialKey; label: string; kind: "special"; repeat?: boolean };

export const ROWS: KeyDef[][] = [
    [
        { name: "Esc", label: "Esc", kind: "special" },
        { name: "/", label: "/", kind: "char" },
        { name: "-", label: "-", kind: "char" },
        { name: "|", label: "|", kind: "char" },
        { name: "Home", label: "Home", kind: "special" },
        { name: "Up", label: "↑", kind: "special", repeat: true },
        { name: "End", label: "End", kind: "special" },
        { name: "PgUp", label: "PgUp", kind: "special", repeat: true },
    ],
    [
        { name: "Tab", label: "Tab", kind: "special" },
        { name: "Ctrl", label: "Ctrl", kind: "mod", mod: "ctrl" },
        { name: "Alt", label: "Alt", kind: "mod", mod: "alt" },
        { name: "Shift", label: "Shift", kind: "mod", mod: "shift" },
        { name: "Left", label: "←", kind: "special", repeat: true },
        { name: "Down", label: "↓", kind: "special", repeat: true },
        { name: "Right", label: "→", kind: "special", repeat: true },
        { name: "PgDn", label: "PgDn", kind: "special", repeat: true },
    ],
];
