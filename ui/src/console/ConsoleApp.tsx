import { useEffect, useRef, useState } from "preact/hooks";
import RFB from "novnc-rfb";
import KeyTable from "novnc-keysym";
import {
  KEY_DEFS,
  KEY_ROWS,
  WIDE_KEYS,
  textKeyStroke,
  type KeyDefinition,
  type KeyStroke,
} from "./keyboard";

const VM_ID = (new URLSearchParams(window.location.search).get("vm") || "").trim();
const WS_URL = VM_ID
  ? `${window.location.protocol === "https:" ? "wss" : "ws"}://${window.location.host}/ws/vms/${encodeURIComponent(VM_ID)}/vnc`
  : "";
const MAX_TYPED_CHARS = 4096;
const TYPE_DELAY_MS = 6;

type ConnectionState = "pending" | "ready" | "idle" | "error";
type Status = {
  label: string;
  state: ConnectionState;
};

type VirtualKeyboardProps = {
  connected: boolean;
  activeModifiers: ReadonlySet<string>;
  onPress: (definition: KeyDefinition) => void;
};

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => window.setTimeout(resolve, ms));
}

function keyClass(definition: KeyDefinition, active: boolean): string {
  const classes = ["vk-key"];
  if (definition.modifier) {
    classes.push("vk-modifier");
  }
  if (active) {
    classes.push("active");
  }
  if (definition.code === "Space") {
    classes.push("vk-space");
  } else if (definition.code === "CtrlAltDel") {
    classes.push("vk-xwide");
  } else if (WIDE_KEYS.has(definition.code)) {
    classes.push("vk-wide");
  }
  return classes.join(" ");
}

function VirtualKeyboard({ connected, activeModifiers, onPress }: VirtualKeyboardProps) {
  return (
    <>
      <p class="vk-hint">Ctrl, Alt, Shift, and Super stay latched until tapped again.</p>
      {KEY_ROWS.map((rowCodes, rowIndex) => (
        <div class="vk-row" key={rowIndex}>
          {rowCodes.map((code) => {
            const definition = KEY_DEFS[code];
            if (!definition) {
              return null;
            }
            const active = activeModifiers.has(code);
            return (
              <button
                key={code}
                type="button"
                class={keyClass(definition, active)}
                title={definition.title || definition.label}
                aria-label={definition.title || definition.label}
                aria-pressed={definition.modifier ? active : undefined}
                disabled={!connected}
                onClick={() => onPress(definition)}
              >
                {definition.label}
              </button>
            );
          })}
        </div>
      ))}
    </>
  );
}

export function ConsoleApp() {
  const [status, setStatus] = useState<Status>({ label: "connecting…", state: "pending" });
  const [error, setError] = useState("");
  const [vmName, setVmName] = useState("VM console");
  const [connected, setConnected] = useState(false);
  const [keyboardOpen, setKeyboardOpen] = useState(false);
  const [activeModifiers, setActiveModifiers] = useState<ReadonlySet<string>>(() => new Set());
  const [clipboardText, setClipboardText] = useState("");
  const [clipboardError, setClipboardError] = useState("");
  const [reconnectDisabled, setReconnectDisabled] = useState(!VM_ID);

  const screenRef = useRef<HTMLElement | null>(null);
  const clipboardDialogRef = useRef<HTMLDialogElement | null>(null);
  const clipboardTextRef = useRef<HTMLTextAreaElement | null>(null);
  const rfbRef = useRef<RFB | null>(null);
  const connectedRef = useRef(false);
  const modifiersRef = useRef<ReadonlySet<string>>(new Set());

  const setStatusLabel = (label: string, state: ConnectionState) => {
    setStatus({ label, state });
  };

  const showError = (message: string) => {
    setError(message);
  };

  const hideError = () => {
    setError("");
  };

  const setConnection = (value: boolean) => {
    connectedRef.current = value;
    setConnected(value);
  };

  const setModifiers = (next: ReadonlySet<string>) => {
    modifiersRef.current = next;
    setActiveModifiers(next);
  };

  const clearModifiers = () => {
    setModifiers(new Set());
  };

  const closeClipboard = () => {
    const dialog = clipboardDialogRef.current;
    if (dialog?.open) {
      dialog.close();
    }
  };

  const showClipboardError = (message: string) => {
    setClipboardError(message);
  };

  const disconnectRFB = () => {
    const current = rfbRef.current;
    rfbRef.current = null;
    if (current) {
      try {
        current.disconnect();
      } catch {
        // Ignore shutdown errors from a stale connection.
      }
    }
  };

  const readBrowserClipboard = async (reportErrors = false, overwrite = true) => {
    if (!navigator.clipboard || typeof navigator.clipboard.readText !== "function") {
      if (reportErrors) {
        showClipboardError(
          "This browser does not expose the clipboard. Paste into the box with Ctrl+V or Cmd+V."
        );
      }
      clipboardTextRef.current?.focus();
      return;
    }

    try {
      const text = await navigator.clipboard.readText();
      if (!overwrite && (clipboardTextRef.current?.value.length ?? 0) > 0) {
        return;
      }
      if (!text) {
        if (reportErrors) {
          showClipboardError("The browser clipboard is empty. Paste into the box with Ctrl+V or Cmd+V.");
        }
        clipboardTextRef.current?.focus();
        return;
      }
      setClipboardText(text);
      setClipboardError("");
      window.requestAnimationFrame(() => {
        clipboardTextRef.current?.focus();
        clipboardTextRef.current?.select();
      });
    } catch {
      if (reportErrors) {
        showClipboardError(
          "Clipboard access was blocked. Paste into the box with Ctrl+V or Cmd+V."
        );
      }
      clipboardTextRef.current?.focus();
    }
  };

  const openClipboard = () => {
    if (!connectedRef.current) {
      return;
    }
    const dialog = clipboardDialogRef.current;
    if (!dialog) {
      return;
    }
    if (dialog.open) {
      clipboardTextRef.current?.focus();
      return;
    }

    setClipboardText("");
    setClipboardError("");
    dialog.showModal();
    window.requestAnimationFrame(() => clipboardTextRef.current?.focus());
    void readBrowserClipboard(false, false);
  };

  const typeTextIntoVM = async () => {
    if (!connectedRef.current) {
      showClipboardError("The console is not connected.");
      return;
    }

    const text = clipboardText.replace(/\r\n?/g, "\n");
    if (!text) {
      showClipboardError("Enter or paste some text first.");
      return;
    }

    const strokes: KeyStroke[] = [];
    const unsupported = new Set<string>();
    let length = 0;
    for (const char of text) {
      length += 1;
      if (length > MAX_TYPED_CHARS) {
        showClipboardError(
          `Type into VM supports at most ${MAX_TYPED_CHARS} characters. Use Set clipboard for larger text.`
        );
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
      const preview = [...unsupported]
        .slice(0, 6)
        .map((char) => JSON.stringify(char))
        .join(", ");
      showClipboardError(
        `The US keyboard layout cannot type ${preview}. Use Set clipboard for full Unicode text.`
      );
      return;
    }

    const current = rfbRef.current;
    if (!current) {
      return;
    }
    closeClipboard();
    current.focus({ preventScroll: true });
    setStatusLabel("typing…", "ready");

    for (const stroke of strokes) {
      if (rfbRef.current !== current || !connectedRef.current) {
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

    if (rfbRef.current === current && connectedRef.current) {
      setStatusLabel("typed into VM", "ready");
      window.setTimeout(() => {
        if (connectedRef.current) {
          setStatusLabel("connected", "ready");
        }
      }, 1200);
    }
  };

  const setVMClipboard = () => {
    if (!connectedRef.current || !rfbRef.current) {
      showClipboardError("The console is not connected.");
      return;
    }

    if (!clipboardText) {
      showClipboardError("Enter or paste some text first.");
      return;
    }

    try {
      rfbRef.current.clipboardPasteFrom(clipboardText);
    } catch (err) {
      showClipboardError(
        `Could not set the VM clipboard: ${err instanceof Error ? err.message : String(err)}`
      );
      return;
    }

    closeClipboard();
    setStatusLabel("clipboard updated", "ready");
    window.setTimeout(() => {
      if (connectedRef.current) {
        setStatusLabel("connected", "ready");
      }
    }, 1200);
  };

  const postVMCommand = async (action: string) => {
    const response = await fetch(`/api/vms/${encodeURIComponent(VM_ID)}/${action}`, {
      method: "POST",
    });
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
  };

  const sendCtrlAltDelVNC = () => {
    const current = rfbRef.current;
    if (!current || !connectedRef.current) {
      return;
    }

    current.focus({ preventScroll: true });
    current.sendKey(KeyTable.XK_Control_L, "ControlLeft", true);
    current.sendKey(KeyTable.XK_Alt_L, "AltLeft", true);

    window.setTimeout(() => {
      if (rfbRef.current !== current) {
        return;
      }
      current.sendKey(KeyTable.XK_Delete, "Delete", true);
      window.setTimeout(() => {
        if (rfbRef.current !== current) {
          return;
        }
        current.sendKey(KeyTable.XK_Delete, "Delete", false);
        current.sendKey(KeyTable.XK_Alt_L, "AltLeft", false);
        current.sendKey(KeyTable.XK_Control_L, "ControlLeft", false);
      }, 120);
    }, 60);
  };

  const sendCtrlAltDel = async () => {
    if (!connectedRef.current) {
      return;
    }

    try {
      // QEMU's monitor injects the key at the hypervisor layer. This avoids
      // any ambiguity in VNC keyboard handling.
      await postVMCommand("ctrl-alt-del");
      setStatusLabel("Ctrl+Alt+Del sent", "ready");
      window.setTimeout(() => {
        if (connectedRef.current) {
          setStatusLabel("connected", "ready");
        }
      }, 900);
    } catch (err) {
      // Older servers or a QEMU process without a monitor: fall back to VNC.
      sendCtrlAltDelVNC();
      setStatusLabel("sent via VNC", "ready");
      showError(
        `Server-side Ctrl+Alt+Del is unavailable (${err instanceof Error ? err.message : String(err)}). ` +
          "Sent the key through VNC instead; restart the VM once to enable the monitor."
      );
      window.setTimeout(() => {
        if (connectedRef.current) {
          setStatusLabel("connected", "ready");
        }
      }, 2500);
    }
  };

  const resetVM = async () => {
    if (!VM_ID) {
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
      setStatusLabel("reset requested", "ready");
      showError("Reset requested. Reconnect after the guest starts again.");
    } catch (err) {
      showError(`Could not reset the VM: ${err instanceof Error ? err.message : String(err)}`);
    }
  };

  const toggleModifier = (definition: KeyDefinition) => {
    const next = new Set(modifiersRef.current);
    if (next.has(definition.code)) {
      next.delete(definition.code);
    } else {
      next.add(definition.code);
    }
    setModifiers(next);
  };

  const pressVirtualKey = (definition: KeyDefinition) => {
    if (!connectedRef.current || !rfbRef.current) {
      return;
    }

    const current = rfbRef.current;
    current.focus({ preventScroll: true });

    if (definition.action === "ctrl-alt-del") {
      void sendCtrlAltDel();
      return;
    }
    if (definition.modifier) {
      toggleModifier(definition);
      return;
    }

    const held = [...modifiersRef.current]
      .map((code) => KEY_DEFS[code])
      .filter((item): item is KeyDefinition => Boolean(item));

    for (const modifier of held) {
      current.sendKey(modifier.keysym, modifier.code, true);
    }
    current.sendKey(definition.keysym, definition.code, true);

    window.setTimeout(() => {
      if (rfbRef.current !== current) {
        return;
      }
      current.sendKey(definition.keysym, definition.code, false);
      for (const modifier of held.slice().reverse()) {
        current.sendKey(modifier.keysym, modifier.code, false);
      }
    }, 60);
  };

  const loadVMInfo = async (): Promise<boolean> => {
    if (!VM_ID) {
      return false;
    }
    try {
      const response = await fetch(`/api/vms/${encodeURIComponent(VM_ID)}`);
      if (!response.ok) {
        return true;
      }
      const vm = await response.json();
      if (!vm || typeof vm.name !== "string") {
        return true;
      }
      setVmName(`${vm.name} console`);
      document.title = `${vm.name} console · rookery`;
      if (vm.console_enabled === false) {
        setStatusLabel("disabled", "error");
        showError("Console access is disabled by the administrator.");
        setReconnectDisabled(true);
        return false;
      }
      if (vm.status !== "running") {
        showError("This VM is not running. Start it from the inventory tab.");
      }
      return true;
    } catch {
      return true;
    }
  };

  const connect = () => {
    if (!VM_ID) {
      return;
    }

    disconnectRFB();
    const screen = screenRef.current;
    if (!screen) {
      return;
    }

    screen.replaceChildren();
    hideError();
    clearModifiers();
    closeClipboard();
    setConnection(false);
    setStatusLabel("connecting…", "pending");

    const current = new RFB(screen, WS_URL, { shared: true });
    rfbRef.current = current;
    current.scaleViewport = true;
    current.resizeSession = false;

    current.addEventListener("connect", () => {
      if (rfbRef.current !== current) {
        return;
      }
      hideError();
      setStatusLabel("connected", "ready");
      setConnection(true);
      current.focus({ preventScroll: true });
    });

    current.addEventListener("disconnect", (event) => {
      if (rfbRef.current !== current) {
        return;
      }
      setConnection(false);
      closeClipboard();
      clearModifiers();
      const clean = Boolean(event.detail?.clean);
      setStatusLabel(clean ? "disconnected" : "connection lost", clean ? "idle" : "error");
      if (!clean) {
        showError("The console connection ended. Check that the VM is running, then reconnect.");
      }
    });

    current.addEventListener("securityfailure", (event) => {
      if (rfbRef.current !== current) {
        return;
      }
      const reason = event.detail?.reason ? `: ${event.detail.reason}` : "";
      setStatusLabel("security failure", "error");
      showError(`VNC security negotiation failed${reason}`);
    });

    current.addEventListener("credentialsrequired", () => {
      if (rfbRef.current !== current) {
        return;
      }
      setStatusLabel("password required", "error");
      showError("This VNC server requires a password, but no password is configured.");
    });
  };

  useEffect(() => {
    if (!VM_ID) {
      setStatusLabel("missing vm id", "error");
      showError("This console link is missing a VM ID.");
      return;
    }

    let cancelled = false;
    void loadVMInfo().then((shouldConnect) => {
      if (!cancelled && shouldConnect) {
        connect();
      }
    });

    const onBeforeUnload = () => {
      disconnectRFB();
    };
    window.addEventListener("beforeunload", onBeforeUnload);

    return () => {
      cancelled = true;
      window.removeEventListener("beforeunload", onBeforeUnload);
      disconnectRFB();
    };
    // The connection helpers only use refs and stable module constants.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const clipboardDisabled = !connected || clipboardText.length === 0;

  return (
    <>
      <header class="console-bar">
        <div class="console-title">
          <span class="brand-mark" aria-hidden="true">◈</span>
          <span id="console-title">{vmName}</span>
        </div>
        <div class="console-actions">
          <span id="console-status" class="console-status" data-state={status.state} role="status">
            {status.label}
          </span>
          <button
            class="console-button"
            id="console-keyboard"
            type="button"
            aria-expanded={keyboardOpen}
            aria-controls="virtual-keyboard"
            onClick={() => setKeyboardOpen((open) => !open)}
          >
            {keyboardOpen ? "Hide keyboard" : "Keyboard"}
          </button>
          <button
            class="console-button"
            id="console-clipboard"
            type="button"
            disabled={!connected}
            title="Paste text into the VM clipboard"
            onClick={openClipboard}
          >
            Paste
          </button>
          <button
            class="console-button"
            id="console-ctrl-alt-del"
            type="button"
            disabled={!connected}
            onClick={() => void sendCtrlAltDel()}
          >
            Ctrl+Alt+Del
          </button>
          <button
            class="console-button"
            id="console-reset"
            type="button"
            disabled={!connected}
            onClick={() => void resetVM()}
          >
            Reset
          </button>
          <button
            class="console-button"
            id="console-reconnect"
            type="button"
            disabled={reconnectDisabled}
            onClick={connect}
          >
            Reconnect
          </button>
          <a class="console-button" href="/">Back to inventory</a>
        </div>
      </header>

      <main
        id="console-screen"
        ref={screenRef}
        class="console-screen"
        aria-label="Remote screen"
      ></main>

      <section
        id="virtual-keyboard"
        class="virtual-keyboard"
        aria-label="Virtual keyboard"
        hidden={!keyboardOpen}
      >
        <VirtualKeyboard
          connected={connected}
          activeModifiers={activeModifiers}
          onPress={pressVirtualKey}
        />
      </section>

      {error ? (
        <p id="console-error" class="console-error" role="alert">
          {error}
        </p>
      ) : null}

      <dialog
        id="clipboard-dialog"
        ref={clipboardDialogRef}
        class="clipboard-dialog"
        aria-labelledby="clipboard-title"
        onClose={() => {
          if (connectedRef.current) {
            rfbRef.current?.focus({ preventScroll: true });
          }
        }}
        onClick={(event) => {
          if (event.target === event.currentTarget) {
            closeClipboard();
          }
        }}
      >
        <form
          class="clipboard-form"
          id="clipboard-form"
          onSubmit={(event) => {
            event.preventDefault();
            void typeTextIntoVM();
          }}
        >
          <header class="clipboard-head">
            <div>
              <p class="clipboard-eyebrow">Clipboard</p>
              <h2 id="clipboard-title">Paste to VM</h2>
            </div>
            <button
              class="clipboard-close"
              id="clipboard-close"
              type="button"
              aria-label="Close clipboard dialog"
              onClick={closeClipboard}
            >
              ×
            </button>
          </header>
          <p class="clipboard-hint">
            <strong>Type into VM</strong> sends the text as US keyboard input, which works
            without guest tools. <strong>Set clipboard</strong> uses VNC's clipboard and
            needs a guest clipboard agent such as <code>spice-vdagent</code>. Browser
            clipboard access may need permission; you can always paste manually below.
          </p>
          <label class="clipboard-field">
            <span>Text</span>
            <textarea
              id="clipboard-text"
              ref={clipboardTextRef}
              rows={8}
              spellcheck={false}
              autocomplete="off"
              autocapitalize="off"
              value={clipboardText}
              placeholder="Paste here with Ctrl+V or Cmd+V"
              onInput={(event) => setClipboardText(event.currentTarget.value)}
            />
          </label>
          {clipboardError ? (
            <p id="clipboard-error" class="clipboard-error" role="alert">
              {clipboardError}
            </p>
          ) : null}
          <footer class="clipboard-actions">
            <button
              class="console-button"
              id="clipboard-read"
              type="button"
              onClick={() => void readBrowserClipboard(true)}
            >
              Read clipboard
            </button>
            <button
              class="console-button"
              id="clipboard-cancel"
              type="button"
              onClick={closeClipboard}
            >
              Cancel
            </button>
            <button
              class="console-button"
              id="clipboard-set"
              type="button"
              disabled={clipboardDisabled}
              onClick={setVMClipboard}
            >
              Set clipboard
            </button>
            <button
              class="console-button clipboard-send"
              id="clipboard-send"
              type="submit"
              disabled={clipboardDisabled}
            >
              Type into VM
            </button>
          </footer>
        </form>
      </dialog>
    </>
  );
}
