import KeyTable from "novnc-keysym";

export type KeyAction = "ctrl-alt-del";

export type KeyDefinition = {
  label: string;
  keysym: number;
  code: string;
  modifier?: boolean;
  action?: KeyAction;
  title?: string;
};

export type KeyStroke = {
  code: string;
  shift?: boolean;
};

function key(
  label: string,
  keysym: number,
  code: string,
  extra: Partial<KeyDefinition> = {}
): KeyDefinition {
  return { label, keysym, code, ...extra };
}

export const KEY_DEFS: Record<string, KeyDefinition> = {
  Escape: key("Esc", KeyTable.XK_Escape, "Escape", { title: "Escape" }),
  Tab: key("Tab", KeyTable.XK_Tab, "Tab"),
  CapsLock: key("Caps", KeyTable.XK_Caps_Lock, "CapsLock", { title: "Caps Lock" }),
  Backspace: key("⌫", KeyTable.XK_BackSpace, "Backspace", { title: "Backspace" }),
  Enter: key("↵", KeyTable.XK_Return, "Enter", { title: "Enter" }),
  Space: key("Space", KeyTable.XK_space, "Space"),
  ContextMenu: key("Menu", KeyTable.XK_Menu, "ContextMenu", { title: "Context menu" }),
  PrintScreen: key("PrtSc", KeyTable.XK_Print, "PrintScreen", { title: "Print Screen" }),
  ScrollLock: key("ScrLk", KeyTable.XK_Scroll_Lock, "ScrollLock", { title: "Scroll Lock" }),
  Pause: key("Pause", KeyTable.XK_Pause, "Pause"),
  Insert: key("Ins", KeyTable.XK_Insert, "Insert", { title: "Insert" }),
  Delete: key("Del", KeyTable.XK_Delete, "Delete", { title: "Delete" }),
  Home: key("Home", KeyTable.XK_Home, "Home"),
  End: key("End", KeyTable.XK_End, "End"),
  PageUp: key("PgUp", KeyTable.XK_Page_Up, "PageUp", { title: "Page Up" }),
  PageDown: key("PgDn", KeyTable.XK_Page_Down, "PageDown", { title: "Page Down" }),
  ArrowLeft: key("←", KeyTable.XK_Left, "ArrowLeft", { title: "Left arrow" }),
  ArrowUp: key("↑", KeyTable.XK_Up, "ArrowUp", { title: "Up arrow" }),
  ArrowDown: key("↓", KeyTable.XK_Down, "ArrowDown", { title: "Down arrow" }),
  ArrowRight: key("→", KeyTable.XK_Right, "ArrowRight", { title: "Right arrow" }),
  Backquote: key("`", KeyTable.XK_grave, "Backquote"),
  Minus: key("-", KeyTable.XK_minus, "Minus"),
  Equal: key("=", KeyTable.XK_equal, "Equal"),
  BracketLeft: key("[", KeyTable.XK_bracketleft, "BracketLeft"),
  BracketRight: key("]", KeyTable.XK_bracketright, "BracketRight"),
  Backslash: key("\\", KeyTable.XK_backslash, "Backslash"),
  Semicolon: key(";", KeyTable.XK_semicolon, "Semicolon"),
  Quote: key("'", KeyTable.XK_apostrophe, "Quote"),
  Comma: key(",", KeyTable.XK_comma, "Comma"),
  Period: key(".", KeyTable.XK_period, "Period"),
  Slash: key("/", KeyTable.XK_slash, "Slash"),
  ShiftLeft: key("Shift", KeyTable.XK_Shift_L, "ShiftLeft", { modifier: true }),
  ShiftRight: key("Shift", KeyTable.XK_Shift_R, "ShiftRight", { modifier: true }),
  ControlLeft: key("Ctrl", KeyTable.XK_Control_L, "ControlLeft", { modifier: true }),
  ControlRight: key("Ctrl", KeyTable.XK_Control_R, "ControlRight", { modifier: true }),
  AltLeft: key("Alt", KeyTable.XK_Alt_L, "AltLeft", { modifier: true }),
  AltRight: key("AltGr", KeyTable.XK_Alt_R, "AltRight", { modifier: true }),
  MetaLeft: key("Super", KeyTable.XK_Meta_L, "MetaLeft", { modifier: true }),
  MetaRight: key("Super", KeyTable.XK_Meta_R, "MetaRight", { modifier: true }),
  CtrlAltDel: key("Ctrl+Alt+Del", 0, "", { action: "ctrl-alt-del" }),
};

for (const letter of "ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
  const code = `Key${letter}`;
  KEY_DEFS[code] = key(letter, KeyTable[`XK_${letter.toLowerCase()}`], code);
}

for (const digit of "1234567890") {
  const code = `Digit${digit}`;
  KEY_DEFS[code] = key(digit, KeyTable[`XK_${digit}`], code);
}

for (let i = 1; i <= 12; i += 1) {
  KEY_DEFS[`F${i}`] = key(`F${i}`, KeyTable[`XK_F${i}`], `F${i}`);
}

export const KEY_ROWS: string[][] = [
  [
    "Escape",
    "F1", "F2", "F3", "F4", "F5", "F6", "F7", "F8", "F9", "F10", "F11", "F12",
    "PrintScreen", "ScrollLock", "Pause",
  ],
  [
    "Backquote",
    "Digit1", "Digit2", "Digit3", "Digit4", "Digit5", "Digit6", "Digit7", "Digit8", "Digit9", "Digit0",
    "Minus", "Equal", "Backspace",
    "Insert", "Home", "PageUp",
  ],
  [
    "Tab",
    "KeyQ", "KeyW", "KeyE", "KeyR", "KeyT", "KeyY", "KeyU", "KeyI", "KeyO", "KeyP",
    "BracketLeft", "BracketRight", "Backslash",
    "Delete", "End", "PageDown",
  ],
  [
    "CapsLock",
    "KeyA", "KeyS", "KeyD", "KeyF", "KeyG", "KeyH", "KeyJ", "KeyK", "KeyL",
    "Semicolon", "Quote", "Enter",
    "ArrowUp",
  ],
  [
    "ShiftLeft",
    "KeyZ", "KeyX", "KeyC", "KeyV", "KeyB", "KeyN", "KeyM",
    "Comma", "Period", "Slash", "ShiftRight",
    "ArrowLeft", "ArrowDown", "ArrowRight",
  ],
  [
    "ControlLeft", "MetaLeft", "AltLeft",
    "CtrlAltDel", "Space", "AltRight", "MetaRight", "ContextMenu", "ControlRight",
  ],
];

export const WIDE_KEYS = new Set([
  "Tab", "CapsLock", "Backspace", "Enter",
  "ShiftLeft", "ShiftRight", "ControlLeft", "ControlRight",
  "AltLeft", "AltRight", "MetaLeft", "MetaRight",
  "PrintScreen", "ScrollLock", "Pause",
  "Insert", "Delete", "Home", "End", "PageUp", "PageDown",
  "ContextMenu", "CtrlAltDel",
]);

const TEXT_KEY_CODES: Record<string, string> = {
  "`": "Backquote",
  "-": "Minus",
  "=": "Equal",
  "[": "BracketLeft",
  "]": "BracketRight",
  "\\": "Backslash",
  ";": "Semicolon",
  "'": "Quote",
  ",": "Comma",
  ".": "Period",
  "/": "Slash",
};

const SHIFTED_TEXT_KEY_CODES: Record<string, string> = {
  "~": "Backquote",
  "!": "Digit1",
  "@": "Digit2",
  "#": "Digit3",
  "$": "Digit4",
  "%": "Digit5",
  "^": "Digit6",
  "&": "Digit7",
  "*": "Digit8",
  "(": "Digit9",
  ")": "Digit0",
  "_": "Minus",
  "+": "Equal",
  "{": "BracketLeft",
  "}": "BracketRight",
  "|": "Backslash",
  ":": "Semicolon",
  '"': "Quote",
  "<": "Comma",
  ">": "Period",
  "?": "Slash",
};

export function textKeyStroke(char: string): KeyStroke | null {
  if (char === "\n") {
    return { code: "Enter" };
  }
  if (char === "\t") {
    return { code: "Tab" };
  }
  if (char === " ") {
    return { code: "Space" };
  }
  if (char >= "a" && char <= "z") {
    return { code: `Key${char.toUpperCase()}` };
  }
  if (char >= "A" && char <= "Z") {
    return { code: `Key${char}`, shift: true };
  }
  if (char >= "0" && char <= "9") {
    return { code: `Digit${char}` };
  }
  if (TEXT_KEY_CODES[char]) {
    return { code: TEXT_KEY_CODES[char] };
  }
  if (SHIFTED_TEXT_KEY_CODES[char]) {
    return { code: SHIFTED_TEXT_KEY_CODES[char], shift: true };
  }
  return null;
}
