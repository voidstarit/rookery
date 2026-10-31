import { useEffect, useRef, useState } from "preact/hooks";
import { FitAddon } from "@xterm/addon-fit";
import { Terminal, type ITheme } from "@xterm/xterm";
import "@fontsource/jetbrains-mono/latin-400.css";
import "@fontsource/jetbrains-mono/latin-700.css";
import "@xterm/xterm/css/xterm.css";
import "./console.css";
import { api } from "../shared/api";
import type { LightVM } from "../inventory/types";

const VM_ID = (new URLSearchParams(window.location.search).get("vm") || "").trim();
const WS_URL = VM_ID
  ? `${window.location.protocol === "https:" ? "wss" : "ws"}://${window.location.host}/ws/lightvms/${encodeURIComponent(VM_ID)}/console`
  : "";

type ConnectionState = "connecting" | "connected" | "closed" | "blocked" | "error";

type ThemeID = "green-black" | "white-black" | "amber-black" | "blue-black" | "solarized-dark" | "light";

type ConsoleTheme = {
  label: string;
  background: string;
  foreground: string;
  muted: string;
  panel: string;
  border: string;
  accent: string;
  xterm: ITheme;
};

const THEME_KEY = "rookery.lightvm.console.theme";
const FONT_KEY = "rookery.lightvm.console.fontSize";
const FONT_SIZES = [12, 13, 14, 16, 18, 20] as const;

const THEMES: Record<ThemeID, ConsoleTheme> = {
  "green-black": {
    label: "Green on black",
    background: "#000000",
    foreground: "#33ff66",
    muted: "#6f9d7a",
    panel: "#061006",
    border: "#14501f",
    accent: "#33ff66",
    xterm: {
      background: "#000000",
      foreground: "#33ff66",
      cursor: "#33ff66",
      cursorAccent: "#000000",
      selectionBackground: "#14532d",
      black: "#001a00",
      red: "#ff6b6b",
      green: "#33ff66",
      yellow: "#c8ff66",
      blue: "#66d9ff",
      magenta: "#d19aff",
      cyan: "#66ffff",
      white: "#d9ffd9",
      brightBlack: "#2f6b3f",
      brightRed: "#ff9292",
      brightGreen: "#7dffa0",
      brightYellow: "#e2ff9c",
      brightBlue: "#9ce6ff",
      brightMagenta: "#e0b9ff",
      brightCyan: "#9cffff",
      brightWhite: "#f2fff2",
    },
  },
  "white-black": {
    label: "White on black",
    background: "#000000",
    foreground: "#f2f2f2",
    muted: "#9a9a9a",
    panel: "#101010",
    border: "#303030",
    accent: "#ffffff",
    xterm: {
      background: "#000000",
      foreground: "#f2f2f2",
      cursor: "#ffffff",
      cursorAccent: "#000000",
      selectionBackground: "#333333",
      black: "#111111",
      red: "#ff6b6b",
      green: "#7bd88f",
      yellow: "#f2cc60",
      blue: "#6cb6ff",
      magenta: "#d2a8ff",
      cyan: "#76e3ea",
      white: "#f2f2f2",
      brightBlack: "#666666",
      brightRed: "#ff9292",
      brightGreen: "#a6f0b3",
      brightYellow: "#ffe08a",
      brightBlue: "#9ccfff",
      brightMagenta: "#e3c4ff",
      brightCyan: "#a9f4f8",
      brightWhite: "#ffffff",
    },
  },
  "amber-black": {
    label: "Amber on black",
    background: "#000000",
    foreground: "#ffb000",
    muted: "#9c7a3a",
    panel: "#140d00",
    border: "#4a3300",
    accent: "#ffb000",
    xterm: {
      background: "#000000",
      foreground: "#ffb000",
      cursor: "#ffb000",
      cursorAccent: "#000000",
      selectionBackground: "#4a3300",
      black: "#1a1000",
      red: "#ff6b3d",
      green: "#d7c04a",
      yellow: "#ffb000",
      blue: "#d9a05b",
      magenta: "#ffcf7a",
      cyan: "#f0d38a",
      white: "#ffe2a8",
      brightBlack: "#7a5a20",
      brightRed: "#ff9b73",
      brightGreen: "#f0d86b",
      brightYellow: "#ffd166",
      brightBlue: "#f0bd7a",
      brightMagenta: "#ffe0a3",
      brightCyan: "#fff0c2",
      brightWhite: "#fff8e3",
    },
  },
  "blue-black": {
    label: "Blue on black",
    background: "#050a14",
    foreground: "#a8d4ff",
    muted: "#6f8db3",
    panel: "#08101f",
    border: "#173963",
    accent: "#8fc7ff",
    xterm: {
      background: "#050a14",
      foreground: "#a8d4ff",
      cursor: "#8fc7ff",
      cursorAccent: "#050a14",
      selectionBackground: "#173963",
      black: "#0b1322",
      red: "#ff7b8a",
      green: "#7bd8b0",
      yellow: "#e8d27a",
      blue: "#8fc7ff",
      magenta: "#c9a7ff",
      cyan: "#7fe6f0",
      white: "#dbeaff",
      brightBlack: "#4f6f99",
      brightRed: "#ffa0ab",
      brightGreen: "#a3f0ce",
      brightYellow: "#f5e6a3",
      brightBlue: "#b8dcff",
      brightMagenta: "#ddc4ff",
      brightCyan: "#b3f5fa",
      brightWhite: "#f1f8ff",
    },
  },
  "solarized-dark": {
    label: "Solarized dark",
    background: "#002b36",
    foreground: "#93a1a1",
    muted: "#657b83",
    panel: "#073642",
    border: "#28505a",
    accent: "#b58900",
    xterm: {
      background: "#002b36",
      foreground: "#93a1a1",
      cursor: "#93a1a1",
      cursorAccent: "#002b36",
      selectionBackground: "#073642",
      black: "#073642",
      red: "#dc322f",
      green: "#859900",
      yellow: "#b58900",
      blue: "#268bd2",
      magenta: "#d33682",
      cyan: "#2aa198",
      white: "#eee8d5",
      brightBlack: "#586e75",
      brightRed: "#cb4b16",
      brightGreen: "#586e75",
      brightYellow: "#657b83",
      brightBlue: "#839496",
      brightMagenta: "#6c71c4",
      brightCyan: "#93a1a1",
      brightWhite: "#fdf6e3",
    },
  },
  light: {
    label: "Black on white",
    background: "#f7f7f2",
    foreground: "#1f2328",
    muted: "#68707a",
    panel: "#ecece6",
    border: "#c5c5bc",
    accent: "#1f6feb",
    xterm: {
      background: "#f7f7f2",
      foreground: "#1f2328",
      cursor: "#1f2328",
      cursorAccent: "#f7f7f2",
      selectionBackground: "#c9d6e3",
      black: "#1f2328",
      red: "#b42318",
      green: "#1a7f37",
      yellow: "#9a6700",
      blue: "#0969da",
      magenta: "#8250df",
      cyan: "#1b7c83",
      white: "#6e7781",
      brightBlack: "#57606a",
      brightRed: "#d1242f",
      brightGreen: "#2da44e",
      brightYellow: "#bf8700",
      brightBlue: "#218bff",
      brightMagenta: "#a475f9",
      brightCyan: "#3192aa",
      brightWhite: "#8c959f",
    },
  },
};

const THEME_ORDER: ThemeID[] = [
  "green-black",
  "white-black",
  "amber-black",
  "blue-black",
  "solarized-dark",
  "light",
];

function storedTheme(): ThemeID {
  const value = window.localStorage.getItem(THEME_KEY);
  return value && value in THEMES ? (value as ThemeID) : "green-black";
}

function storedFontSize(): number {
  const value = Number(window.localStorage.getItem(FONT_KEY));
  return FONT_SIZES.includes(value as (typeof FONT_SIZES)[number]) ? value : 14;
}

const stateLabels: Record<ConnectionState, string> = {
  connecting: "connecting",
  connected: "connected",
  closed: "disconnected",
  blocked: "unavailable",
  error: "error",
};

const stateBadges: Record<ConnectionState, string> = {
  connecting: "starting",
  connected: "running",
  closed: "disabled",
  blocked: "error",
  error: "error",
};

export function LightVMConsoleApp() {
  const [state, setState] = useState<ConnectionState>("connecting");
  const [message, setMessage] = useState("Connecting to the LightVM console…");
  const [name, setName] = useState("");
  const [themeID, setThemeID] = useState<ThemeID>(storedTheme);
  const [fontSize, setFontSize] = useState<number>(storedFontSize);
  const [connectionKey, setConnectionKey] = useState(0);
  const containerRef = useRef<HTMLDivElement | null>(null);
  const terminalRef = useRef<Terminal | null>(null);
  const fitRef = useRef<FitAddon | null>(null);
  const socketRef = useRef<WebSocket | null>(null);

  useEffect(() => {
    const theme = THEMES[themeID];
    const root = document.documentElement;
    root.style.setProperty("--term-bg", theme.background);
    root.style.setProperty("--term-fg", theme.foreground);
    root.style.setProperty("--term-muted", theme.muted);
    root.style.setProperty("--term-panel", theme.panel);
    root.style.setProperty("--term-border", theme.border);
    root.style.setProperty("--term-accent", theme.accent);
    window.localStorage.setItem(THEME_KEY, themeID);
    const terminal = terminalRef.current;
    if (terminal) {
      terminal.options.theme = theme.xterm;
    }
  }, [themeID]);

  useEffect(() => {
    window.localStorage.setItem(FONT_KEY, String(fontSize));
    const terminal = terminalRef.current;
    if (terminal) {
      terminal.options.fontSize = fontSize;
      fitRef.current?.fit();
    }
  }, [fontSize]);

  useEffect(() => {
    if (!containerRef.current) {
      return;
    }
    const terminal = new Terminal({
      cursorBlink: true,
      cursorStyle: "block",
      cursorInactiveStyle: "block",
      convertEol: false,
      scrollback: 5000,
      fontFamily:
        '"JetBrains Mono", "Cascadia Mono", "Fira Code", "SFMono-Regular", "Liberation Mono", Consolas, monospace',
      fontSize,
      fontWeight: "400",
      fontWeightBold: "700",
      lineHeight: 1.15,
      theme: THEMES[themeID].xterm,
    });
    const fit = new FitAddon();
    terminal.loadAddon(fit);
    terminal.open(containerRef.current);
    terminalRef.current = terminal;
    fitRef.current = fit;
    requestAnimationFrame(() => {
      fit.fit();
      terminal.focus();
    });

    const focusTerminal = () => terminal.focus();
    containerRef.current.addEventListener("pointerdown", focusTerminal);

    const input = terminal.onData((data) => {
      const socket = socketRef.current;
      if (socket && socket.readyState === WebSocket.OPEN) {
        socket.send(data);
      }
    });
    const onResize = () => {
      fit.fit();
    };
    window.addEventListener("resize", onResize);
    return () => {
      window.removeEventListener("resize", onResize);
      containerRef.current?.removeEventListener("pointerdown", focusTerminal);
      input.dispose();
      terminal.dispose();
      terminalRef.current = null;
      fitRef.current = null;
    };
    // Terminal is created once; theme/font updates are applied separately.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    const terminal = terminalRef.current;
    if (!terminal) {
      return;
    }
    if (!VM_ID) {
      setState("error");
      setMessage("No LightVM was selected. Open the console from the LightVM inventory.");
      terminal.writeln("\r\nNo LightVM was selected.");
      return;
    }

    let active = true;
    let socket: WebSocket | null = null;
    const decoder = new TextDecoder();
    setState("connecting");
    setMessage("Checking LightVM state…");
    terminal.writeln("\r\nConnecting to LightVM console…");

    const connect = async () => {
      try {
        const vm = await api<LightVM>(`/api/lightvm/instances/${encodeURIComponent(VM_ID)}`);
        if (!active) {
          return;
        }
        setName(vm.name || "");
        if (!vm.console_enabled) {
          setState("blocked");
          setMessage("Console access is disabled for this LightVM.");
          terminal.writeln("\r\nConsole access is disabled for this LightVM.");
          return;
        }
        if (vm.status !== "running") {
          setState("blocked");
          setMessage("Start the LightVM before opening its console.");
          terminal.writeln("\r\nStart the LightVM before opening its console.");
          return;
        }

        socket = new WebSocket(WS_URL);
        socket.binaryType = "arraybuffer";
        socketRef.current = socket;
        socket.onopen = () => {
          if (!active) {
            return;
          }
          setState("connected");
          setMessage("Connected to the virtio console. Input is sent to the guest.");
          terminal.focus();
          fitRef.current?.fit();
        };
        socket.onmessage = (event) => {
          if (typeof event.data === "string") {
            terminal.write(event.data);
          } else if (event.data instanceof ArrayBuffer) {
            terminal.write(decoder.decode(new Uint8Array(event.data), { stream: true }));
          }
        };
        socket.onerror = () => {
          if (!active) {
            return;
          }
          setState("error");
          setMessage("The console connection failed.");
          terminal.writeln("\r\n[console connection failed]");
        };
        socket.onclose = () => {
          if (!active) {
            return;
          }
          socketRef.current = null;
          setState("closed");
          setMessage("Console disconnected.");
          terminal.writeln("\r\n[console disconnected]");
        };
      } catch (err) {
        if (!active) {
          return;
        }
        setState("error");
        const detail = err instanceof Error ? err.message : String(err);
        setMessage(detail);
        terminal.writeln(`\r\nCould not open the console: ${detail}`);
      }
    };
    void connect();

    return () => {
      active = false;
      if (socket) {
        socket.close();
      }
      socketRef.current = null;
    };
  }, [connectionKey]);

  return (
    <main class="lightvm-console">
      <header class="lightvm-console-head">
        <div>
          <p class="eyebrow">LightVM console</p>
          <h1>{name || VM_ID || "No LightVM selected"}</h1>
          {name && VM_ID ? <p class="lightvm-console-hint">{VM_ID}</p> : null}
        </div>
        <div class="lightvm-console-actions">
          <label class="console-select">
            <span>Theme</span>
            <select
              value={themeID}
              onChange={(event) => setThemeID(event.currentTarget.value as ThemeID)}
            >
              {THEME_ORDER.map((id) => (
                <option value={id} key={id}>
                  {THEMES[id].label}
                </option>
              ))}
            </select>
          </label>
          <label class="console-select">
            <span>Font</span>
            <select
              value={String(fontSize)}
              onChange={(event) => setFontSize(Number(event.currentTarget.value))}
            >
              {FONT_SIZES.map((size) => (
                <option value={String(size)} key={size}>
                  {size}px
                </option>
              ))}
            </select>
          </label>
          <span class="status-badge" data-status={stateBadges[state]}>
            {stateLabels[state]}
          </span>
          <button
            type="button"
            disabled={!VM_ID || state === "connecting" || state === "connected"}
            onClick={() => setConnectionKey((value) => value + 1)}
          >
            Reconnect
          </button>
          <a href="/lightvm/instances">Back to LightVMs</a>
        </div>
      </header>
      <p class="lightvm-console-hint">{message}</p>
      <div class="terminal-wrap" ref={containerRef} />
    </main>
  );
}
