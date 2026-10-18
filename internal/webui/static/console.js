import RFB from "/novnc/core/rfb.js";
import KeyTable from "/novnc/core/input/keysym.js";

const params = new URLSearchParams(window.location.search);
const vmID = (params.get("vm") || "").trim();

const screen = document.getElementById("console-screen");
const title = document.getElementById("console-title");
const statusEl = document.getElementById("console-status");
const errorEl = document.getElementById("console-error");
const keyboardEl = document.getElementById("virtual-keyboard");
const keyboardButton = document.getElementById("console-keyboard");
const ctrlAltDel = document.getElementById("console-ctrl-alt-del");
const resetButton = document.getElementById("console-reset");
const reconnect = document.getElementById("console-reconnect");
const clipboardButton = document.getElementById("console-clipboard");
const clipboardDialog = document.getElementById("clipboard-dialog");
const clipboardText = document.getElementById("clipboard-text");
const clipboardError = document.getElementById("clipboard-error");
const clipboardRead = document.getElementById("clipboard-read");
const clipboardSet = document.getElementById("clipboard-set");
const clipboardSend = document.getElementById("clipboard-send");
const clipboardCancel = document.getElementById("clipboard-cancel");
const clipboardClose = document.getElementById("clipboard-close");
const clipboardForm = document.getElementById("clipboard-form");

let rfb = null;
let wsURL = "";
let keyboardEnabled = false;
const activeModifiers = new Set();

function key(label, keysym, code, extra = {}) {
  return { label, keysym, code, ...extra };
}

const KEY_DEFS = {
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

const KEY_ROWS = [
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

const WIDE_KEYS = new Set([
  "Tab", "CapsLock", "Backspace", "Enter",
  "ShiftLeft", "ShiftRight", "ControlLeft", "ControlRight",
  "AltLeft", "AltRight", "MetaLeft", "MetaRight",
  "PrintScreen", "ScrollLock", "Pause",
  "Insert", "Delete", "Home", "End", "PageUp", "PageDown",
  "ContextMenu", "CtrlAltDel",
]);

function setStatus(label, state) {
  statusEl.textContent = label;
  statusEl.dataset.state = state;
}

function showError(message) {
  errorEl.textContent = message;
  errorEl.hidden = false;
}

function hideError() {
  errorEl.hidden = true;
  errorEl.textContent = "";
}

function isConnected() {
  return Boolean(rfb) && statusEl.dataset.state === "ready";
}

function setClipboardEnabled(enabled) {
  clipboardButton.disabled = !enabled;
  if (!enabled && clipboardDialog.open) {
    clipboardDialog.close();
  }
}

function showClipboardError(message) {
  clipboardError.textContent = message;
  clipboardError.hidden = false;
}

function hideClipboardError() {
  clipboardError.hidden = true;
  clipboardError.textContent = "";
}

function updateClipboardSendState() {
  const disabled = !isConnected() || clipboardText.value.length === 0;
  clipboardSet.disabled = disabled;
  clipboardSend.disabled = disabled;
}

async function readBrowserClipboard(reportErrors = false, overwrite = true) {
  if (!navigator.clipboard || typeof navigator.clipboard.readText !== "function") {
    if (reportErrors) {
      showClipboardError(
        "This browser does not expose the clipboard. Paste into the box with Ctrl+V or Cmd+V."
      );
    }
    clipboardText.focus();
    return;
  }

  try {
    const text = await navigator.clipboard.readText();
    if (!overwrite && clipboardText.value.length > 0) {
      return;
    }
    if (!text) {
      if (reportErrors) {
        showClipboardError("The browser clipboard is empty. Paste into the box with Ctrl+V or Cmd+V.");
      }
      clipboardText.focus();
      return;
    }
    clipboardText.value = text;
    hideClipboardError();
    updateClipboardSendState();
    clipboardText.focus();
    clipboardText.select();
  } catch {
    if (reportErrors) {
      showClipboardError(
        "Clipboard access was blocked. Paste into the box with Ctrl+V or Cmd+V."
      );
    }
    clipboardText.focus();
  }
}

async function openClipboardDialog() {
  if (!isConnected()) {
    return;
  }
  if (clipboardDialog.open) {
    clipboardText.focus();
    return;
  }

  clipboardText.value = "";
  hideClipboardError();
  updateClipboardSendState();
  clipboardDialog.showModal();
  clipboardText.focus();
  await readBrowserClipboard(false, false);
}

const TEXT_KEY_CODES = {
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

const SHIFTED_TEXT_KEY_CODES = {
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

const MAX_TYPED_CHARS = 4096;
const TYPE_DELAY_MS = 6;

function textKeyStroke(char) {
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

function delay(ms) {
  return new Promise((resolve) => window.setTimeout(resolve, ms));
}

async function typeTextIntoVM() {
  if (!isConnected()) {
    showClipboardError("The console is not connected.");
    return;
  }

  const text = clipboardText.value.replace(/\r\n?/g, "\n");
  if (!text) {
    showClipboardError("Enter or paste some text first.");
    return;
  }

  const strokes = [];
  const unsupported = new Set();
  let length = 0;
  for (const char of text) {
    length += 1;
    if (length > MAX_TYPED_CHARS) {
      showClipboardError(`Type into VM supports at most ${MAX_TYPED_CHARS} characters. Use Set clipboard for larger text.`);
      return;
    }
    const stroke = textKeyStroke(char);
    if (stroke) {
      strokes.push(stroke);
    } else {
      unsupported.add(char);
    }
  }

  if (unsupported.size > 0) {
    const preview = [...unsupported].slice(0, 6).map((char) => JSON.stringify(char)).join(", ");
    showClipboardError(
      `The US keyboard layout cannot type ${preview}. Use Set clipboard for full Unicode text.`
    );
    return;
  }

  const current = rfb;
  clipboardDialog.close();
  current.focus({ preventScroll: true });
  setStatus("typing…", "ready");

  for (const stroke of strokes) {
    if (rfb !== current || !isConnected()) {
      return;
    }
    const definition = KEY_DEFS[stroke.code];
    if (!definition) {
      continue;
    }
    if (stroke.shift) {
      current.sendKey(KEY_DEFS.ShiftLeft.keysym, "ShiftLeft", true);
    }
    current.sendKey(definition.keysym, definition.code, true);
    current.sendKey(definition.keysym, definition.code, false);
    if (stroke.shift) {
      current.sendKey(KEY_DEFS.ShiftLeft.keysym, "ShiftLeft", false);
    }
    await delay(TYPE_DELAY_MS);
  }

  if (rfb === current && isConnected()) {
    setStatus("typed into VM", "ready");
    window.setTimeout(() => {
      if (isConnected()) {
        setStatus("connected", "ready");
      }
    }, 1200);
  }
}

function setVMClipboard() {
  if (!isConnected()) {
    showClipboardError("The console is not connected.");
    return;
  }

  const text = clipboardText.value;
  if (!text) {
    showClipboardError("Enter or paste some text first.");
    return;
  }

  try {
    rfb.clipboardPasteFrom(text);
  } catch (err) {
    showClipboardError(`Could not set the VM clipboard: ${err.message}`);
    return;
  }

  clipboardDialog.close();
  setStatus("clipboard updated", "ready");
  window.setTimeout(() => {
    if (isConnected()) {
      setStatus("connected", "ready");
    }
  }, 1200);
}

function setKeyboardEnabled(enabled) {
  keyboardEnabled = enabled;
  for (const button of keyboardEl.querySelectorAll("button")) {
    button.disabled = !enabled;
  }
}

function updateModifierButtons() {
  for (const button of keyboardEl.querySelectorAll(".vk-modifier")) {
    const active = activeModifiers.has(button.dataset.code);
    button.classList.toggle("active", active);
    button.setAttribute("aria-pressed", String(active));
  }
}

function clearModifiers() {
  activeModifiers.clear();
  updateModifierButtons();
}

function buildKeyboard() {
  keyboardEl.replaceChildren();

  const hint = document.createElement("p");
  hint.className = "vk-hint";
  hint.textContent = "Ctrl, Alt, Shift, and Super stay latched until tapped again.";
  keyboardEl.append(hint);

  for (const rowCodes of KEY_ROWS) {
    const row = document.createElement("div");
    row.className = "vk-row";

    for (const code of rowCodes) {
      const definition = KEY_DEFS[code];
      if (!definition) {
        continue;
      }

      const button = document.createElement("button");
      button.type = "button";
      button.className = "vk-key";
      button.dataset.code = code;
      button.textContent = definition.label;
      button.title = definition.title || definition.label;
      button.setAttribute("aria-label", definition.title || definition.label);
      button.disabled = !keyboardEnabled;

      if (definition.modifier) {
        button.classList.add("vk-modifier");
        button.setAttribute("aria-pressed", "false");
      }
      if (definition.action) {
        button.dataset.action = definition.action;
      }
      if (code === "Space") {
        button.classList.add("vk-space");
      } else if (code === "CtrlAltDel") {
        button.classList.add("vk-xwide");
      } else if (WIDE_KEYS.has(code)) {
        button.classList.add("vk-wide");
      }

      row.append(button);
    }
    keyboardEl.append(row);
  }
}

async function postVMCommand(action) {
  const response = await fetch(
    `/api/vms/${encodeURIComponent(vmID)}/${action}`,
    { method: "POST" }
  );
  if (!response.ok) {
    let message = `Request failed (${response.status})`;
    try {
      const data = await response.json();
      if (data && typeof data.error === "string") {
        message = data.error;
      }
    } catch {
      // Keep the status-based message.
    }
    throw new Error(message);
  }
}

function sendCtrlAltDelVNC() {
  if (!isConnected()) {
    return;
  }

  const current = rfb;
  current.focus({ preventScroll: true });
  current.sendKey(KeyTable.XK_Control_L, "ControlLeft", true);
  current.sendKey(KeyTable.XK_Alt_L, "AltLeft", true);

  window.setTimeout(() => {
    if (rfb !== current) {
      return;
    }
    current.sendKey(KeyTable.XK_Delete, "Delete", true);
    window.setTimeout(() => {
      if (rfb !== current) {
        return;
      }
      current.sendKey(KeyTable.XK_Delete, "Delete", false);
      current.sendKey(KeyTable.XK_Alt_L, "AltLeft", false);
      current.sendKey(KeyTable.XK_Control_L, "ControlLeft", false);
    }, 120);
  }, 60);
}

async function sendCtrlAltDel() {
  if (!isConnected()) {
    return;
  }

  try {
    // QEMU's monitor injects the key at the hypervisor layer. This avoids
    // any ambiguity in VNC keyboard handling.
    await postVMCommand("ctrl-alt-del");
    setStatus("Ctrl+Alt+Del sent", "ready");
    window.setTimeout(() => {
      if (isConnected()) {
        setStatus("connected", "ready");
      }
    }, 900);
  } catch (err) {
    // Older servers or a QEMU process without a monitor: fall back to VNC.
    sendCtrlAltDelVNC();
    setStatus("sent via VNC", "ready");
    showError(
      `Server-side Ctrl+Alt+Del is unavailable (${err.message}). ` +
        "Sent the key through VNC instead; restart the VM once to enable the monitor."
    );
    window.setTimeout(() => {
      if (isConnected()) {
        setStatus("connected", "ready");
      }
    }, 2500);
  }
}

async function resetVM() {
  if (!vmID) {
    return;
  }
  const confirmed = window.confirm(
    "Reset this VM?\n\nUnsaved guest data may be lost and the console will disconnect."
  );
  if (!confirmed) {
    return;
  }

  try {
    await postVMCommand("reset");
    setStatus("reset requested", "ready");
    showError("Reset requested. Reconnect after the guest starts again.");
  } catch (err) {
    showError(`Could not reset the VM: ${err.message}`);
  }
}

function toggleModifier(definition) {
  if (activeModifiers.has(definition.code)) {
    activeModifiers.delete(definition.code);
  } else {
    activeModifiers.add(definition.code);
  }
  updateModifierButtons();
}

function pressVirtualKey(definition) {
  if (!isConnected()) {
    return;
  }

  const current = rfb;
  current.focus({ preventScroll: true });

  if (definition.action === "ctrl-alt-del") {
    sendCtrlAltDel();
    return;
  }
  if (definition.modifier) {
    toggleModifier(definition);
    return;
  }

  const held = [...activeModifiers]
    .map((code) => KEY_DEFS[code])
    .filter(Boolean);

  for (const modifier of held) {
    current.sendKey(modifier.keysym, modifier.code, true);
  }
  current.sendKey(definition.keysym, definition.code, true);

  window.setTimeout(() => {
    if (rfb !== current) {
      return;
    }
    current.sendKey(definition.keysym, definition.code, false);
    for (const modifier of held.slice().reverse()) {
      current.sendKey(modifier.keysym, modifier.code, false);
    }
  }, 60);
}

if (!vmID) {
  setStatus("missing vm id", "error");
  showError("This console link is missing a VM ID.");
  reconnect.disabled = true;
} else {
  const scheme = window.location.protocol === "https:" ? "wss" : "ws";
  wsURL = `${scheme}://${window.location.host}/ws/vms/${encodeURIComponent(vmID)}/vnc`;

  loadVMInfo().then((vm) => {
    if (vm) {
      title.textContent = `${vm.name} console`;
      document.title = `${vm.name} console · nerds-vms`;
      if (vm.console_enabled === false) {
        setStatus("disabled", "error");
        showError("Console access is disabled for this VM by the administrator.");
        reconnect.disabled = true;
        return;
      }
      if (vm.status !== "running") {
        showError("This VM is not running. Start it from the inventory tab.");
      }
    }
    connect();
  });
}

async function loadVMInfo() {
  try {
    const response = await fetch(`/api/vms/${encodeURIComponent(vmID)}`);
    return response.ok ? response.json() : null;
  } catch {
    return null;
  }
}

function connect() {
  if (!vmID) {
    return;
  }

  if (rfb) {
    const previous = rfb;
    rfb = null;
    try {
      previous.disconnect();
    } catch {
      // Ignore shutdown errors from a stale connection.
    }
  }

  screen.replaceChildren();
  hideError();
  clearModifiers();
  setKeyboardEnabled(false);
  setClipboardEnabled(false);
  setStatus("connecting…", "pending");
  ctrlAltDel.disabled = true;
  resetButton.disabled = true;

  const current = new RFB(screen, wsURL, { shared: true });
  rfb = current;
  current.scaleViewport = true;
  current.resizeSession = false;

  current.addEventListener("connect", () => {
    if (rfb !== current) {
      return;
    }
    hideError();
    setStatus("connected", "ready");
    ctrlAltDel.disabled = false;
    resetButton.disabled = false;
    setKeyboardEnabled(true);
    setClipboardEnabled(true);
    current.focus({ preventScroll: true });
  });

  current.addEventListener("disconnect", (event) => {
    if (rfb !== current) {
      return;
    }
    ctrlAltDel.disabled = true;
    resetButton.disabled = true;
    setKeyboardEnabled(false);
    setClipboardEnabled(false);
    clearModifiers();
    const clean = Boolean(event.detail && event.detail.clean);
    setStatus(clean ? "disconnected" : "connection lost", clean ? "idle" : "error");
    if (!clean) {
      showError("The console connection ended. Check that the VM is running, then reconnect.");
    }
  });

  current.addEventListener("securityfailure", (event) => {
    if (rfb !== current) {
      return;
    }
    const reason = event.detail && event.detail.reason ? `: ${event.detail.reason}` : "";
    setStatus("security failure", "error");
    showError(`VNC security negotiation failed${reason}`);
  });

  current.addEventListener("credentialsrequired", () => {
    if (rfb !== current) {
      return;
    }
    setStatus("password required", "error");
    showError("This VNC server requires a password, but no password is configured.");
  });
}

buildKeyboard();
updateModifierButtons();

keyboardEl.addEventListener("click", (event) => {
  const button = event.target.closest("button[data-code]");
  if (!button || button.disabled) {
    return;
  }
  pressVirtualKey(KEY_DEFS[button.dataset.code]);
});

keyboardButton.addEventListener("click", () => {
  const opening = keyboardEl.hidden;
  keyboardEl.hidden = !opening;
  keyboardButton.setAttribute("aria-expanded", String(opening));
  keyboardButton.textContent = opening ? "Hide keyboard" : "Keyboard";
});

ctrlAltDel.addEventListener("click", sendCtrlAltDel);
resetButton.addEventListener("click", resetVM);

clipboardButton.addEventListener("click", () => {
  openClipboardDialog();
});
clipboardForm.addEventListener("submit", (event) => {
  event.preventDefault();
  typeTextIntoVM();
});
clipboardText.addEventListener("input", updateClipboardSendState);
clipboardRead.addEventListener("click", () => {
  readBrowserClipboard(true);
});
clipboardSet.addEventListener("click", setVMClipboard);
clipboardSend.addEventListener("click", typeTextIntoVM);
clipboardCancel.addEventListener("click", () => clipboardDialog.close());
clipboardClose.addEventListener("click", () => clipboardDialog.close());
clipboardDialog.addEventListener("click", (event) => {
  if (event.target === clipboardDialog) {
    clipboardDialog.close();
  }
});
clipboardDialog.addEventListener("close", () => {
  if (isConnected()) {
    rfb.focus({ preventScroll: true });
  }
});

reconnect.addEventListener("click", () => {
  if (vmID) {
    connect();
  }
});

window.addEventListener("beforeunload", () => {
  if (rfb) {
    try {
      rfb.disconnect();
    } catch {
      // Nothing to do while the page is closing.
    }
  }
});
