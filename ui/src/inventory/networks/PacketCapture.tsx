import { useEffect, useState } from "preact/hooks";
import { api } from "../../shared/api";
import { showToast } from "../../shared/toast";
import type {
  PacketCaptureFile,
  PacketCaptureOverview,
  PacketCaptureStatus,
  VPPInterface,
} from "../types";

type CaptureForm = {
  interface: string;
  direction: string;
  maxMB: number;
  snapLength: number;
};

const DEFAULT_FORM: CaptureForm = {
  interface: "any",
  direction: "both",
  maxMB: 10,
  snapLength: 2048,
};

function formatBytes(value: number): string {
  if (!Number.isFinite(value) || value < 0) {
    return "—";
  }
  if (value < 1024) {
    return `${value} B`;
  }
  if (value < 1024 * 1024) {
    return `${(value / 1024).toFixed(1)} KiB`;
  }
  if (value < 1024 * 1024 * 1024) {
    return `${(value / (1024 * 1024)).toFixed(1)} MiB`;
  }
  return `${(value / (1024 * 1024 * 1024)).toFixed(2)} GiB`;
}

function formatDate(value?: string): string {
  if (!value) {
    return "—";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return date.toLocaleString();
}

function directionLabel(value?: string): string {
  switch (value) {
    case "rx":
      return "RX only";
    case "tx":
      return "TX only";
    case "both":
    case "rx/tx":
      return "RX + TX";
    default:
      return value || "—";
  }
}

function captureDownloadURL(name: string): string {
  return `/api/troubleshoot/capture/files/${encodeURIComponent(name)}`;
}

function webSharkURL(name: string): string {
  return `/webshark/?capture=${encodeURIComponent(name)}&embed=1`;
}

export function PacketCapturePanel() {
  const [interfaces, setInterfaces] = useState<VPPInterface[]>([]);
  const [status, setStatus] = useState<PacketCaptureStatus | null>(null);
  const [files, setFiles] = useState<PacketCaptureFile[]>([]);
  const [form, setForm] = useState<CaptureForm>(DEFAULT_FORM);
  const [loading, setLoading] = useState(true);
  const [starting, setStarting] = useState(false);
  const [stopping, setStopping] = useState(false);
  const [deleting, setDeleting] = useState("");
  const [error, setError] = useState("");
  const [selected, setSelected] = useState("");

  const estimatedPackets = Math.min(
    1_000_000,
    Math.max(1, Math.ceil((form.maxMB * 1024 * 1024) / (form.snapLength + 16)))
  );
  const estimatedBytes = estimatedPackets * (form.snapLength + 16);
  const formValid =
    form.maxMB >= 1 && form.maxMB <= 100 && form.snapLength >= 64 && form.snapLength <= 9000;
  const active = Boolean(status?.active);

  const loadFiles = async () => {
    const next = await api<PacketCaptureFile[]>("/api/troubleshoot/capture/files");
    setFiles(next);
    if (selected && !next.some((file) => file.name === selected)) {
      setSelected("");
    }
  };

  const refresh = async () => {
    setLoading(true);
    setError("");
    const [interfacesResult, overviewResult] = await Promise.allSettled([
      api<VPPInterface[]>("/api/troubleshoot/capture/interfaces"),
      api<PacketCaptureOverview>("/api/troubleshoot/capture"),
    ]);
    if (interfacesResult.status === "fulfilled") {
      setInterfaces(interfacesResult.value);
    } else {
      setError(
        interfacesResult.reason instanceof Error
          ? interfacesResult.reason.message
          : String(interfacesResult.reason)
      );
    }
    if (overviewResult.status === "fulfilled") {
      setStatus(overviewResult.value.status);
      setFiles(overviewResult.value.files);
    } else {
      if (!error) {
        setError(
          overviewResult.reason instanceof Error
            ? overviewResult.reason.message
            : String(overviewResult.reason)
        );
      }
      try {
        await loadFiles();
      } catch {
        // Keep the status error; capture files may still be downloadable.
      }
    }
    setLoading(false);
  };

  useEffect(() => {
    void refresh();
    // The capture panel loads independently of the graph.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (!active) {
      return;
    }
    let cancelled = false;
    const timer = window.setInterval(async () => {
      try {
        const overview = await api<PacketCaptureOverview>("/api/troubleshoot/capture");
        if (cancelled) {
          return;
        }
        setStatus(overview.status);
        setFiles(overview.files);
        setError("");
        if (!overview.status.active) {
          setSelected((current) =>
            current && overview.files.some((file) => file.name === current) ? current : ""
          );
        }
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : String(err));
        }
      }
    }, 1500);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
    // loadFiles only uses state setters and the current selected value.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active]);

  const startCapture = async () => {
    if (!formValid) {
      return;
    }
    setStarting(true);
    setError("");
    try {
      const next = await api<PacketCaptureStatus>("/api/troubleshoot/capture", {
        method: "POST",
        body: JSON.stringify({
          interface: form.interface,
          direction: form.direction,
          max_bytes: Math.round(form.maxMB * 1024 * 1024),
          snap_length: form.snapLength,
        }),
      });
      setStatus(next);
      setSelected("");
      showToast("Packet capture started");
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      setError(message);
      showToast(message, true);
    } finally {
      setStarting(false);
    }
  };

  const deleteCapture = async (file: PacketCaptureFile) => {
    if (active && status?.file === file.name) {
      return;
    }
    if (!window.confirm(`Delete ${file.name}?`)) {
      return;
    }
    setDeleting(file.name);
    setError("");
    try {
      await api<null>(captureDownloadURL(file.name), { method: "DELETE" });
      setFiles((current) => current.filter((item) => item.name !== file.name));
      setStatus((current) =>
        current && current.file === file.name ? { ...current, file: "" } : current
      );
      if (selected === file.name) {
        setSelected("");
      }
      showToast("Packet capture deleted");
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      setError(message);
      showToast(message, true);
    } finally {
      setDeleting("");
    }
  };

  const stopCapture = async () => {
    setStopping(true);
    setError("");
    try {
      const next = await api<PacketCaptureStatus>("/api/troubleshoot/capture/stop", {
        method: "POST",
      });
      setStatus(next);
      try {
        await loadFiles();
      } catch {
        // The stop result still identifies the file even if listing fails.
      }
      if (next.file) {
        setSelected(next.file);
      }
      showToast("Packet capture stopped");
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      setError(message);
      showToast(message, true);
    } finally {
      setStopping(false);
    }
  };

  return (
    <div class="capture-panel">
      <div class="capture-toolbar">
        <span class={active ? "capture-badge active" : "capture-badge"}>
          {active ? "Capturing" : "Idle"}
        </span>
        <button
          class="button ghost"
          type="button"
          disabled={loading || active || starting || stopping}
          onClick={() => void refresh()}
        >
          Refresh
        </button>
      </div>

      {error ? (
        <div class="banner" role="alert">
          {error}
        </div>
      ) : null}

      <div class="capture-form">
        <label>
          <span>Interface</span>
          <select
            value={form.interface}
            disabled={active}
            onChange={(event) =>
              setForm((current) => ({ ...current, interface: event.currentTarget.value }))
            }
          >
            <option value="any">Any interface</option>
            {interfaces.map((iface) => (
              <option key={iface.name} value={iface.name}>
                {iface.name} · #{iface.sw_if_index}
                {iface.type ? ` · ${iface.type}` : ""}
                {iface.up ? "" : " · down"}
              </option>
            ))}
          </select>
        </label>

        <label>
          <span>Direction</span>
          <select
            value={form.direction}
            disabled={active}
            onChange={(event) =>
              setForm((current) => ({ ...current, direction: event.currentTarget.value }))
            }
          >
            <option value="both">RX + TX</option>
            <option value="rx">RX only</option>
            <option value="tx">TX only</option>
          </select>
        </label>

        <label>
          <span>Max size (MB)</span>
          <input
            type="number"
            min={1}
            max={100}
            step={1}
            value={form.maxMB}
            disabled={active}
            onChange={(event) =>
              setForm((current) => ({ ...current, maxMB: Number(event.currentTarget.value) }))
            }
          />
        </label>

        <label>
          <span>Snap length (bytes)</span>
          <input
            type="number"
            min={64}
            max={9000}
            step={64}
            value={form.snapLength}
            disabled={active}
            onChange={(event) =>
              setForm((current) => ({ ...current, snapLength: Number(event.currentTarget.value) }))
            }
          />
        </label>
      </div>

      <p class="field-hint">
        Approx. {formatBytes(estimatedBytes)} /{" "}
        {estimatedPackets.toLocaleString()} packets. VPP keeps up to the computed packet
        count and writes the pcap under the host’s <code>/tmp</code>; only traffic on the
        selected interface is captured.
      </p>

      <div class="capture-actions">
        {active ? (
          <button
            class="button primary"
            type="button"
            disabled={stopping}
            onClick={() => void stopCapture()}
          >
            {stopping ? "Stopping…" : "Stop capture"}
          </button>
        ) : (
          <button
            class="button primary"
            type="button"
            disabled={!formValid || starting || loading}
            onClick={() => void startCapture()}
          >
            {starting ? "Starting…" : "Start capture"}
          </button>
        )}
        {status?.file ? (
          <span class="capture-current-file">
            {active ? "Writing" : "Last capture"}: <code>{status.file}</code>
          </span>
        ) : null}
      </div>

      {status ? (
        <div class={active ? "capture-status active" : "capture-status"}>
          <div class="capture-status-head">
            <strong>{active ? "Capture running" : "No capture running"}</strong>
            {status.interface ? <span>{status.interface}</span> : null}
          </div>
          <div class="capture-status-grid">
            <span>
              <b>Direction</b>
              {directionLabel(status.direction)}
            </span>
            <span>
              <b>Size</b>
              {formatBytes(status.size)}
            </span>
            <span>
              <b>{active ? "Buffer" : "Captured"}</b>
              {active
                ? `${status.buffered.toLocaleString()} / ${status.max_packets.toLocaleString()} packets`
                : `${status.packets.toLocaleString()} packets`}
            </span>
            <span>
              <b>Limit</b>
              {status.max_packets.toLocaleString()} packets · {formatBytes(status.max_bytes)}
            </span>
            <span>
              <b>Started</b>
              {formatDate(status.started_at)}
            </span>
            {status.stopped_at ? (
              <span>
                <b>Stopped</b>
                {formatDate(status.stopped_at)}
              </span>
            ) : null}
          </div>
          {status.raw ? (
            <details class="capture-raw">
              <summary>VPP output</summary>
              <pre>{status.raw}</pre>
            </details>
          ) : null}
        </div>
      ) : null}

      <div class="capture-files">
        <div class="capture-files-head">
          <h3>Captures</h3>
          <span>
            {files.length} file{files.length === 1 ? "" : "s"}
          </span>
        </div>
        {files.length === 0 ? (
          <p class="field-hint">
            No captures yet. Start a capture, then stop it to inspect the pcap.
          </p>
        ) : (
          <div class="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>File</th>
                  <th>Size</th>
                  <th>Modified</th>
                  <th>Actions</th>
                </tr>
              </thead>
              <tbody>
                {files.map((file) => (
                  <tr key={file.name} class={selected === file.name ? "capture-selected" : ""}>
                    <td>
                      <code>{file.name}</code>
                    </td>
                    <td>{formatBytes(file.size)}</td>
                    <td>{formatDate(file.modified)}</td>
                    <td>
                      <div class="capture-file-actions">
                        <button class="button ghost" type="button" onClick={() => setSelected(file.name)}>
                          View
                        </button>
                        <a class="button ghost" href={captureDownloadURL(file.name)} download>
                          Download
                        </a>
                        <button
                          class="button ghost danger"
                          type="button"
                          disabled={deleting === file.name || (active && status?.file === file.name)}
                          title={active && status?.file === file.name ? "Stop the capture before deleting it" : undefined}
                          onClick={() => void deleteCapture(file)}
                        >
                          {deleting === file.name ? "Deleting…" : "Delete"}
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {selected ? (
        <div class="capture-viewer">
          <div class="capture-viewer-head">
            <span>
              WebShark · <code>{selected}</code>
            </span>
            <div class="capture-file-actions">
              <a class="button ghost" href={webSharkURL(selected)} target="_blank" rel="noreferrer">
                New tab
              </a>
              <a class="button ghost" href={captureDownloadURL(selected)} download>
                Download
              </a>
              <button class="button ghost" type="button" onClick={() => setSelected("")}>
                Close
              </button>
            </div>
          </div>
          <iframe
            key={selected}
            class="capture-viewer-frame"
            src={webSharkURL(selected)}
            title={`WebShark packet capture ${selected}`}
            allow="fullscreen"
          />
        </div>
      ) : null}
    </div>
  );
}
