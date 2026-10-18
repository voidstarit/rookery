import { useEffect, useRef, useState } from "preact/hooks";
import { api } from "../../shared/api";
import type { VM, VMMetrics } from "../types";
import {
  drawMetricChart,
  formatBytes,
  formatDuration,
  formatMemory,
  type MonitorSample,
} from "./monitor";

type VMMonitorDialogProps = {
  vm: VM;
  onClose: () => void;
};

type LastSample = {
  at: number;
  cpu: number;
  read: number;
  write: number;
  netRead: number;
  netWrite: number;
};

export function VMMonitorDialog({ vm, onClose }: VMMonitorDialogProps) {
  const [data, setData] = useState<VMMetrics | null>(null);
  const [samples, setSamples] = useState<MonitorSample[]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const loadingRef = useRef(false);
  const dialogRef = useRef<HTMLDialogElement | null>(null);
  const lastRef = useRef<LastSample | null>(null);
  const cpuCanvas = useRef<HTMLCanvasElement | null>(null);
  const memoryCanvas = useRef<HTMLCanvasElement | null>(null);
  const ioCanvas = useRef<HTMLCanvasElement | null>(null);
  const networkCanvas = useRef<HTMLCanvasElement | null>(null);

  const load = async () => {
    if (loadingRef.current) {
      return;
    }
    loadingRef.current = true;
    setLoading(true);
    try {
      const next = await api<VMMetrics>(`/api/vms/${encodeURIComponent(vm.id)}/metrics`);
      const now = Date.now();
      const cpuTotal = Number(next.cpu_seconds_total) || 0;
      const readTotal = Number(next.disk_read_bytes_total) || 0;
      const writeTotal = Number(next.disk_write_bytes_total) || 0;
      const netReadTotal = Number(next.network_rx_bytes_total) || 0;
      const netWriteTotal = Number(next.network_tx_bytes_total) || 0;
      const last = lastRef.current;
      let cpuPercent: number | null = null;
      let readRate: number | null = null;
      let writeRate: number | null = null;
      let netReadRate: number | null = null;
      let netWriteRate: number | null = null;

      if (last && now > last.at) {
        const seconds = (now - last.at) / 1000;
        if (cpuTotal >= last.cpu) {
          cpuPercent = ((cpuTotal - last.cpu) / seconds) * 100;
        }
        if (readTotal >= last.read) {
          readRate = (readTotal - last.read) / seconds;
        }
        if (writeTotal >= last.write) {
          writeRate = (writeTotal - last.write) / seconds;
        }
        if (netReadTotal >= last.netRead) {
          netReadRate = (netReadTotal - last.netRead) / seconds;
        }
        if (netWriteTotal >= last.netWrite) {
          netWriteRate = (netWriteTotal - last.netWrite) / seconds;
        }
      }

      if (!next.running) {
        lastRef.current = null;
        setSamples([]);
      } else {
        lastRef.current = {
          at: now,
          cpu: cpuTotal,
          read: readTotal,
          write: writeTotal,
          netRead: netReadTotal,
          netWrite: netWriteTotal,
        };
        setSamples((current) => {
          const updated = [
            ...current,
            {
              cpu: cpuPercent,
              rss: Number(next.memory_rss_bytes) || 0,
              read: readRate,
              write: writeRate,
              netRead: netReadRate,
              netWrite: netWriteRate,
            },
          ];
          return updated.length > 60 ? updated.slice(updated.length - 60) : updated;
        });
      }
      setData(next);
      setError("");
    } catch (err) {
      setError(`Could not read VM metrics: ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      loadingRef.current = false;
      setLoading(false);
    }
  };

  useEffect(() => {
    dialogRef.current?.showModal();
    void load();
    const timer = window.setInterval(() => {
      if (!document.hidden) {
        void load();
      }
    }, 2000);
    return () => {
      window.clearInterval(timer);
    };
    // The dialog is remounted for each VM.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    const cpuValues = samples.map((sample) => sample.cpu);
    const memoryValues = samples.map((sample) => sample.rss);
    const readValues = samples.map((sample) => sample.read);
    const writeValues = samples.map((sample) => sample.write);
    const netReadValues = samples.map((sample) => sample.netRead);
    const netWriteValues = samples.map((sample) => sample.netWrite);
    const finiteCPU = cpuValues.filter((value): value is number => Number.isFinite(value));
    const finiteMemory = memoryValues.filter((value): value is number => Number.isFinite(value));
    const finiteIO = [...readValues, ...writeValues].filter((value): value is number =>
      Number.isFinite(value)
    );
    const finiteNet = [...netReadValues, ...netWriteValues].filter((value): value is number =>
      Number.isFinite(value)
    );
    const configuredBytes = Math.max(0, Number(vm.memory_mb || 0) * 1024 * 1024);

    drawMetricChart(cpuCanvas.current, [{ values: cpuValues, color: "#60a5fa" }], {
      max: Math.max(100, finiteCPU.length ? Math.max(...finiteCPU) * 1.15 : 0),
      format: (value) => `${Math.round(value)}%`,
    });
    drawMetricChart(memoryCanvas.current, [{ values: memoryValues, color: "#34d399" }], {
      max: Math.max(configuredBytes, finiteMemory.length ? Math.max(...finiteMemory) * 1.15 : 0),
      marker: configuredBytes,
      format: formatBytes,
    });
    drawMetricChart(
      ioCanvas.current,
      [
        { values: readValues, color: "#f59e0b" },
        { values: writeValues, color: "#f472b6" },
      ],
      {
        max: finiteIO.length ? Math.max(...finiteIO) * 1.15 : 0,
        format: (value) => `${formatBytes(value)}/s`,
      }
    );
    drawMetricChart(
      networkCanvas.current,
      [
        { values: netReadValues, color: "#38bdf8" },
        { values: netWriteValues, color: "#a78bfa" },
      ],
      {
        max: finiteNet.length ? Math.max(...finiteNet) * 1.15 : 0,
        format: (value) => `${formatBytes(value)}/s`,
      }
    );
  }, [samples, vm.memory_mb]);

  const close = () => {
    dialogRef.current?.close();
  };

  const current = data;
  const running = Boolean(current?.running);

  return (
    <dialog
      ref={dialogRef}
      id="vm-monitor-dialog"
      aria-labelledby="vm-monitor-title"
      onClose={onClose}
      onClick={(event) => {
        if (event.target === event.currentTarget) {
          close();
        }
      }}
    >
      <header class="dialog-head">
        <div>
          <p class="eyebrow">Live telemetry</p>
          <h2 id="vm-monitor-title">VM monitor · {vm.name}</h2>
        </div>
        <button class="icon-button" type="button" aria-label="Close monitor" onClick={close}>
          ×
        </button>
      </header>

      <p class="dialog-lede">QEMU process metrics</p>
      {error ? (
        <div class="banner" role="alert">
          {error}
        </div>
      ) : null}

      <div class="monitor-grid">
        <section class="monitor-card">
          <span class="metric-label">Process</span>
          <strong class="metric-value" data-status={running ? "running" : "stopped"}>
            {running ? "running" : "stopped"}
          </strong>
          <span class="metric-detail">PID {current?.pid || "—"}</span>
        </section>
        <section class="monitor-card">
          <span class="metric-label">CPU</span>
          <strong class="metric-value">
            {!running ? "—" : samples.length <= 1 ? "sampling…" : `${(samples[samples.length - 1]?.cpu ?? 0).toFixed(1)}%`}
          </strong>
          <span class="metric-detail">{current ? `${current.vcpus} vCPU` : "—"}</span>
        </section>
        <section class="monitor-card">
          <span class="metric-label">Memory</span>
          <strong class="metric-value">
            {running ? formatBytes(Number(current?.memory_rss_bytes) || 0) : "—"}
          </strong>
          <span class="metric-detail">
            {current
              ? `${formatMemory(current.memory_mb)} configured${
                  running ? ` · ${formatBytes(Number(current.memory_virtual_bytes) || 0)} virtual` : ""
                }`
              : "—"}
          </span>
        </section>
        <section class="monitor-card">
          <span class="metric-label">Disk read</span>
          <strong class="metric-value">
            {running
              ? samples.length <= 1
                ? "sampling…"
                : `${formatBytes(samples[samples.length - 1]?.read ?? 0)}/s`
              : "—"}
          </strong>
          <span class="metric-detail">
            {current ? `${formatBytes(Number(current.disk_read_bytes_total) || 0)} total` : "—"}
          </span>
        </section>
        <section class="monitor-card">
          <span class="metric-label">Disk write</span>
          <strong class="metric-value">
            {running
              ? samples.length <= 1
                ? "sampling…"
                : `${formatBytes(samples[samples.length - 1]?.write ?? 0)}/s`
              : "—"}
          </strong>
          <span class="metric-detail">
            {current ? `${formatBytes(Number(current.disk_write_bytes_total) || 0)} total` : "—"}
          </span>
        </section>
        <section class="monitor-card">
          <span class="metric-label">Network I/O</span>
          <strong class="metric-value">
            {running
              ? samples.length <= 1
                ? "sampling…"
                : `↓ ${formatBytes(samples[samples.length - 1]?.netRead ?? 0)}/s · ↑ ${formatBytes(
                    samples[samples.length - 1]?.netWrite ?? 0
                  )}/s`
              : "—"}
          </strong>
          <span class="metric-detail">
            {current
              ? `RX ${formatBytes(Number(current.network_rx_bytes_total) || 0)} · TX ${formatBytes(
                  Number(current.network_tx_bytes_total) || 0
                )}`
              : "—"}
          </span>
        </section>
        <section class="monitor-card">
          <span class="metric-label">Uptime</span>
          <strong class="metric-value">
            {running && current ? formatDuration(Number(current.uptime_seconds) || 0) : "—"}
          </strong>
          <span class="metric-detail">since QEMU started</span>
        </section>
      </div>

      <div class="monitor-charts">
        <section class="chart-card">
          <div class="chart-head">
            <span>CPU history</span>
            <span class="chart-note">last 60 samples</span>
          </div>
          <canvas
            ref={cpuCanvas}
            class="metric-chart"
            role="img"
            aria-label="CPU usage history"
          ></canvas>
        </section>
        <section class="chart-card">
          <div class="chart-head">
            <span>Memory RSS</span>
            <span class="chart-note">last 60 samples</span>
          </div>
          <canvas
            ref={memoryCanvas}
            class="metric-chart"
            role="img"
            aria-label="Memory RSS history"
          ></canvas>
        </section>
        <section class="chart-card">
          <div class="chart-head">
            <span>Disk I/O</span>
            <span class="chart-legend">
              <i class="legend-read"></i>read <i class="legend-write"></i>write
            </span>
          </div>
          <canvas
            ref={ioCanvas}
            class="metric-chart"
            role="img"
            aria-label="Disk read and write history"
          ></canvas>
        </section>
        <section class="chart-card">
          <div class="chart-head">
            <span>Network I/O</span>
            <span class="chart-legend">
              <i class="legend-net-rx"></i>RX <i class="legend-net-tx"></i>TX
            </span>
          </div>
          <canvas
            ref={networkCanvas}
            class="metric-chart"
            role="img"
            aria-label="Network receive and transmit history"
          ></canvas>
        </section>
      </div>

      <p class="field-hint">
        {current && running
          ? `Sampled ${new Date(current.sampled_at).toLocaleTimeString()}`
          : "VM is not running."}
      </p>

      <footer class="dialog-actions">
        <button class="button ghost" type="button" disabled={loading} onClick={() => void load()}>
          {loading ? "Refreshing…" : "Refresh"}
        </button>
        <button class="button primary" type="button" onClick={close}>
          Close
        </button>
      </footer>
    </dialog>
  );
}
