import { useEffect, useRef, useState } from "preact/hooks";
import { api } from "../../shared/api";
import { showToast } from "../../shared/toast";
import type { CgroupClass, CgroupIOLimit, CgroupSettings, CgroupUsage, HostBlockDevice } from "../types";
import { CPUSelector, IOLimitEditor } from "../cgroup";
import { useRoute } from "../router";

const MIB = 1024 * 1024;

function bytesToMB(value: number | undefined): string {
  if (!value) {
    return "";
  }
  return String(Math.round(value / MIB));
}

function mbToBytes(value: string): number {
  const parsed = Number(value);
  if (!Number.isFinite(parsed) || parsed <= 0) {
    return 0;
  }
  return Math.round(parsed * MIB);
}

function weightValue(value: number | undefined): string {
  return value ? String(value) : "";
}

function limitNumber(value: string): number {
  const parsed = Number(value);
  return Number.isFinite(parsed) && parsed > 0 ? Math.floor(parsed) : 0;
}

type UsageRates = Record<string, { cpuCores: number; read: number; write: number }>;

type UsageSample = {
  at: number;
  nodes: Record<string, { cpu: number; read: number; write: number }>;
};

function formatBytes(value: number | undefined): string {
  const bytes = Number(value) || 0;
  if (bytes >= 1024 ** 3) {
    return `${(bytes / 1024 ** 3).toFixed(1)} GB`;
  }
  if (bytes >= 1024 ** 2) {
    return `${(bytes / 1024 ** 2).toFixed(1)} MB`;
  }
  if (bytes >= 1024) {
    return `${(bytes / 1024).toFixed(1)} KB`;
  }
  return `${bytes} B`;
}

function formatRate(value: number | undefined): string {
  return value === undefined ? "—" : `${formatBytes(value)}/s`;
}

function usageMemoryText(node: { memory_current_bytes: number; memory_max_bytes: number }): string {
  const current = formatBytes(node.memory_current_bytes);
  return node.memory_max_bytes > 0 ? `${current} / ${formatBytes(node.memory_max_bytes)}` : `${current} / unlimited`;
}

function usagePercent(current: number, maximum: number): number {
  if (maximum <= 0) {
    return 0;
  }
  return Math.max(0, Math.min(100, (current / maximum) * 100));
}

export function CgroupSettingsPanel() {
  const [settings, setSettings] = useState<CgroupSettings | null>(null);
  const [devices, setDevices] = useState<HostBlockDevice[]>([]);
  const [enabled, setEnabled] = useState(false);
  const [cpus, setCPUs] = useState("");
  const [mems, setMems] = useState("");
  const [memoryMB, setMemoryMB] = useState("");
  const [cpuWeight, setCPUWeight] = useState("");
  const [ioLimits, setIOLimits] = useState<CgroupIOLimit[]>([]);
  const [classes, setClasses] = useState<CgroupClass[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [usage, setUsage] = useState<CgroupUsage | null>(null);
  const [usageRates, setUsageRates] = useState<UsageRates>({});
  const [usageError, setUsageError] = useState("");
  const [usageLoading, setUsageLoading] = useState(false);
  const usageSample = useRef<UsageSample | null>(null);
  const route = useRoute();

  const apply = (data: CgroupSettings) => {
    setSettings(data);
    setEnabled(Boolean(data.enabled));
    setCPUs(data.cpus || "");
    setMems(data.mems || "");
    setMemoryMB(bytesToMB(data.memory_max_bytes));
    setCPUWeight(weightValue(data.cpu_weight));
    setIOLimits(Array.isArray(data.io_limits) ? data.io_limits : []);
    setClasses(Array.isArray(data.classes) ? data.classes : []);
  };

  const load = async () => {
    setLoading(true);
    try {
      const [config, blockDevices] = await Promise.all([
        api<CgroupSettings>("/api/cgroup-settings"),
        api<HostBlockDevice[]>("/api/host/block-devices").catch(() => []),
      ]);
      apply(config);
      setDevices(Array.isArray(blockDevices) ? blockDevices : []);
      setError("");
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  };

  const loadUsage = async () => {
    setUsageLoading(true);
    try {
      const data = await api<CgroupUsage>("/api/cgroup-usage");
      const at = data.sampled_at ? new Date(data.sampled_at).getTime() : Date.now();
      const nodes = Array.isArray(data.nodes) ? data.nodes : [];
      const previous = usageSample.current;
      const nextRates: UsageRates = {};
      if (previous && Number.isFinite(at) && at > previous.at) {
        const seconds = (at - previous.at) / 1000;
        for (const node of nodes) {
          const path = node.path || "";
          const before = previous.nodes[path];
          if (!before) {
            continue;
          }
          nextRates[path] = {
            cpuCores: Math.max(0, (node.cpu_usage_usec - before.cpu) / 1_000_000 / seconds),
            read: Math.max(0, (node.io_read_bytes - before.read) / seconds),
            write: Math.max(0, (node.io_write_bytes - before.write) / seconds),
          };
        }
      }
      usageSample.current = {
        at: Number.isFinite(at) ? at : Date.now(),
        nodes: Object.fromEntries(
          nodes.map((node) => [
            node.path || "",
            { cpu: node.cpu_usage_usec, read: node.io_read_bytes, write: node.io_write_bytes },
          ])
        ),
      };
      setUsageRates(nextRates);
      setUsage(data);
      setUsageError("");
    } catch (err) {
      setUsageError(err instanceof Error ? err.message : String(err));
    } finally {
      setUsageLoading(false);
    }
  };

  useEffect(() => {
    void load();
  }, []);

  const settingsVisible = route.segments[0] === "settings";
  useEffect(() => {
    if (!settingsVisible) {
      return;
    }
    void loadUsage();
    const timer = window.setInterval(() => {
      if (!document.hidden) {
        void loadUsage();
      }
    }, 2000);
    return () => window.clearInterval(timer);
  }, [settingsVisible]);

  const save = async (event: Event) => {
    event.preventDefault();
    if (saving) {
      return;
    }
    setSaving(true);
    setError("");
    try {
      const updated = await api<CgroupSettings>("/api/cgroup-settings", {
        method: "PUT",
        body: JSON.stringify({
          enabled,
          cpus: cpus.trim(),
          mems: mems.trim(),
          memory_max_bytes: mbToBytes(memoryMB),
          cpu_weight: limitNumber(cpuWeight),
          io_limits: ioLimits.map((limit) => ({
            device: limit.device,
            read_bps: limit.read_bps || 0,
            write_bps: limit.write_bps || 0,
            read_iops: limit.read_iops || 0,
            write_iops: limit.write_iops || 0,
            weight: limit.weight || 0,
          })),
          classes: classes.map((item) => ({
            name: item.name.trim(),
            cpus: (item.cpus || "").trim(),
            memory_max_bytes: item.memory_max_bytes || 0,
            cpu_weight: item.cpu_weight || 0,
            io_limits: (item.io_limits || []).map((limit) => ({
              device: limit.device,
              read_bps: limit.read_bps || 0,
              write_bps: limit.write_bps || 0,
              read_iops: limit.read_iops || 0,
              write_iops: limit.write_iops || 0,
              weight: limit.weight || 0,
            })),
          })),
        }),
      });
      apply(updated);
      void loadUsage();
      showToast(enabled ? "Saved cgroup resource settings" : "Cgroup limits disabled");
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  };

  const onlineCPUs = Array.isArray(settings?.online_cpus) ? settings.online_cpus : [];
  const available = Boolean(settings?.available);
  const enabledNow = Boolean(settings?.enabled);
  const statusReason = settings?.reason || "";
  const statusDetail = enabledNow
    ? settings?.configured
      ? `Applied at ${settings?.root || "/sys/fs/cgroup/rookery-cgroup"}`
      : "Enabled but not applied yet"
    : "Disabled";

  const usageNodes = Array.isArray(usage?.nodes) ? usage.nodes : [];
  const rootUsage = usageNodes.find((node) => !(node.path || "")) || null;
  const leafUsage = usageNodes.filter((node) => {
    const path = node.path || "";
    if (!path) {
      return false;
    }
    return !usageNodes.some((other) => {
      const otherPath = other.path || "";
      return otherPath !== "" && otherPath.startsWith(`${path}/`);
    });
  });
  const summaryUsage = leafUsage.length > 0 ? leafUsage : rootUsage ? [rootUsage] : [];
  const summaryCPU = summaryUsage.reduce((total, node) => total + (usageRates[node.path || ""]?.cpuCores || 0), 0);
  const summaryRead = summaryUsage.reduce((total, node) => total + (usageRates[node.path || ""]?.read || 0), 0);
  const summaryWrite = summaryUsage.reduce((total, node) => total + (usageRates[node.path || ""]?.write || 0), 0);
  const online = usage?.online_cpus || onlineCPUs.length || 0;
  const cpuPercent = online > 0 ? (summaryCPU / online) * 100 : 0;
  const memoryPercent = rootUsage ? usagePercent(rootUsage.memory_current_bytes, rootUsage.memory_max_bytes) : 0;
  const usageStatus = usage?.sampled_at
    ? `sampled ${new Date(usage.sampled_at).toLocaleTimeString()}`
    : "waiting for sample";

  return (
    <section class="panel panel-spaced" aria-labelledby="cgroup-settings-heading">
      <div class="panel-head">
        <div>
          <h2 id="cgroup-settings-heading">Cgroup v2 resources</h2>
          <p class="panel-subtitle">Host-wide ceilings inherited by every QEMU process</p>
        </div>
        <button class="button ghost" type="button" disabled={loading || saving} onClick={() => { void load(); void loadUsage(); }}>
          {loading ? "Refreshing…" : "Refresh"}
        </button>
      </div>

      <p class="field-hint">
        Rookery runs QEMU below {settings?.root || "/sys/fs/cgroup/rookery-cgroup"}. VMs without a cgroup
        class run in the shared child cgroup; each class is a cgroup node below the
        <code>rookery-cgroup</code> root, for example <code>rookery-cgroup/prod/web</code>.
      </p>

      <div class="cgroup-status">
        <span class="status-badge" data-status={available ? (enabledNow ? (settings?.configured ? "running" : "paused") : "disabled") : "error"}>
          {available ? (enabledNow ? (settings?.configured ? "applied" : "not applied") : "disabled") : "unavailable"}
        </span>
        <span class="field-hint">
          {statusDetail}
          {settings?.mount ? ` · mount ${settings.mount}` : ""}
          {onlineCPUs.length > 0 ? ` · ${onlineCPUs.length} online CPUs` : ""}
          {classes.length > 0 ? ` · ${classes.length} classes` : ""}
          {settings?.cpu_model ? ` · ${settings.cpu_model}` : ""}
        </span>
      </div>
      {statusReason ? (
        <div class="banner" role="alert">
          {statusReason}
        </div>
      ) : null}

      <section class="cgroup-telemetry" aria-labelledby="cgroup-usage-heading">
        <div class="panel-head">
          <div>
            <h3 id="cgroup-usage-heading">Live cgroup usage</h3>
            <p class="panel-subtitle">
              Memory, CPU, task, and I/O telemetry from the running hierarchy · {usageStatus}
            </p>
          </div>
          <button class="button ghost" type="button" disabled={usageLoading} onClick={() => void loadUsage()}>
            {usageLoading ? "Sampling…" : "Refresh usage"}
          </button>
        </div>

        {usageError ? (
          <div class="banner" role="alert">
            {usageError}
          </div>
        ) : !usage ? (
          <p class="field-hint">Waiting for the first cgroup sample…</p>
        ) : !usage.available ? (
          <p class="field-hint">cgroup v2 is not available on this host.</p>
        ) : !usage.configured ? (
          <p class="field-hint">
            {usage.reason || "Enable cgroup limits to start collecting resource usage."}
          </p>
        ) : usageNodes.length === 0 ? (
          <p class="field-hint">{usage.reason || "No cgroup nodes are available yet."}</p>
        ) : (
          <>
            <div class="cgroup-metrics">
              <div class="cgroup-metric">
                <span class="field-label">Memory</span>
                <strong>{rootUsage ? formatBytes(rootUsage.memory_current_bytes) : "—"}</strong>
                <span class="field-hint">
                  {rootUsage && rootUsage.memory_max_bytes > 0
                    ? `of ${formatBytes(rootUsage.memory_max_bytes)} · ${memoryPercent.toFixed(1)}%`
                    : "unlimited"}
                </span>
                {rootUsage && rootUsage.memory_max_bytes > 0 ? (
                  <div class="usage-bar">
                    <span style={{ width: `${memoryPercent}%` }} />
                  </div>
                ) : null}
              </div>
              <div class="cgroup-metric">
                <span class="field-label">CPU</span>
                <strong>{summaryCPU.toFixed(2)} cores</strong>
                <span class="field-hint">
                  {online > 0 ? `${cpuPercent.toFixed(1)}% of ${online} online CPUs` : "online CPU count unavailable"}
                </span>
              </div>
              <div class="cgroup-metric">
                <span class="field-label">Tasks</span>
                <strong>{rootUsage?.pids_current ?? "—"}</strong>
                <span class="field-hint">
                  {rootUsage && rootUsage.pids_max > 0
                    ? `of ${rootUsage.pids_max}`
                    : "no task limit"}
                </span>
              </div>
              <div class="cgroup-metric">
                <span class="field-label">I/O</span>
                <strong>{formatRate(summaryRead)}</strong>
                <span class="field-hint">read · {formatRate(summaryWrite)} write</span>
              </div>
            </div>

            <div class="table-wrap">
              <table class="cgroup-usage-table">
                <thead>
                  <tr>
                    <th scope="col">Scope</th>
                    <th scope="col">Memory</th>
                    <th scope="col">CPU</th>
                    <th scope="col">Tasks</th>
                    <th scope="col">I/O</th>
                  </tr>
                </thead>
                <tbody>
                  {usageNodes.map((node) => {
                    const path = node.path || "";
                    const rate = usageRates[path];
                    return (
                      <tr key={path || "root"}>
                        <td>
                          <span class="vm-name mono">{node.name || (path || "rookery-cgroup")}</span>
                          {path ? <span class="vm-description mono">{path}</span> : null}
                        </td>
                        <td class="mono">{usageMemoryText(node)}</td>
                        <td class="mono">
                          {rate ? `${rate.cpuCores.toFixed(2)} cores` : "—"}
                        </td>
                        <td class="mono">
                          {node.pids_current}
                          {node.pids_max > 0 ? ` / ${node.pids_max}` : ""}
                        </td>
                        <td class="mono">
                          {rate ? `↓ ${formatRate(rate.read)} · ↑ ${formatRate(rate.write)}` : "—"}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          </>
        )}
      </section>

      <form class="form-grid" onSubmit={(event) => void save(event)}>
        <label class="field field-wide checkbox-field">
          <span class="checkbox-row">
            <input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.currentTarget.checked)} />
            <span>Enable cgroup v2 limits for QEMU</span>
          </span>
          <span class="field-hint">
            Saving applies the new host defaults to every running VM immediately: Rookery re-places
            each live QEMU process in its shared or class cgroup. New and restarted VMs are always
            placed under this hierarchy. When disabled, running QEMU processes are moved out
            immediately and the Rookery cgroups are removed; all VMs then run unconstrained, and
            class limits take effect again when host limits are enabled. Shrinking the host CPU set
            clamps running VMs to the remaining CPUs.
          </span>
        </label>

        <div class="field field-wide">
          <span class="field-label">CPU cores</span>
          <CPUSelector
            value={cpus}
            options={onlineCPUs}
            disabled={!enabled}
            onChange={setCPUs}
            hint="Click cores to pin QEMU to them. The text field accepts ranges. Empty means all online CPUs."
          />
        </div>

        <label class="field">
          <span class="field-label">Total memory limit (MB)</span>
          <input
            type="number"
            min={0}
            step={128}
            disabled={!enabled}
            value={memoryMB}
            placeholder="unlimited"
            onInput={(event) => setMemoryMB(event.currentTarget.value)}
          />
          <span class="field-hint">memory.max for the whole Rookery cgroup subtree. Empty means unlimited.</span>
        </label>

        <label class="field">
          <span class="field-label">CPU weight</span>
          <input
            type="number"
            min={0}
            max={10000}
            step={1}
            disabled={!enabled}
            value={cpuWeight}
            placeholder="default (100)"
            onInput={(event) => setCPUWeight(event.currentTarget.value)}
          />
          <span class="field-hint">cpu.weight (1–10000). Empty uses the kernel default of 100.</span>
        </label>

        <label class="field">
          <span class="field-label">Memory nodes <em>optional</em></span>
          <input
            type="text"
            disabled={!enabled}
            value={mems}
            placeholder="e.g. 0-1 (empty means all)"
            onInput={(event) => setMems(event.currentTarget.value)}
          />
          <span class="field-hint">cpuset.mems NUMA node list.</span>
        </label>

        <div class="field field-wide">
          <span class="field-label">Per-device I/O limits</span>
          <IOLimitEditor limits={ioLimits} devices={devices} disabled={!enabled} onChange={setIOLimits} />
          {devices.length === 0 ? (
            <span class="field-hint">
              No whole block devices were reported by the host. Check access to <code>/sys/class/block</code>.
            </span>
          ) : null}
          <span class="field-hint">
            io.max limits read/write bandwidth (MiB/s) and IOPS. io.weight sets the proportional
            weight for that device (1–10000; requires the BFQ scheduler).
          </span>
        </div>

        <div class="field field-wide">
          <span class="field-label">Classes</span>
          <span class="field-hint">
            Define a tree of reusable profiles below <code>rookery-cgroup</code>. Class paths are
            slash-separated and may be up to 3 levels deep, for example <code>prod</code>,{" "}
            <code>prod/web</code>, or <code>prod/web/frontend</code>. Define the parent class first.
            VMs can only be assigned to leaf classes; parent classes group their children and inherit
            limits. Assign a class to a VM instead of entering limits per VM. Class limits are shared
            by every VM assigned to that class; allow enough memory for all of them.
          </span>
          <CgroupClassEditor
            classes={classes}
            devices={devices}
            onlineCPUs={onlineCPUs}
            disabled={loading || saving}
            onChange={setClasses}
          />
        </div>

        {error ? (
          <p class="form-error field-wide" role="alert">
            {error}
          </p>
        ) : null}

        <div class="field-wide actions">
          <button class="button primary" type="submit" disabled={saving || loading}>
            {saving ? "Saving…" : "Save cgroup settings"}
          </button>
        </div>
      </form>
    </section>
  );
}

function nextClassName(classes: CgroupClass[], parent?: string): string {
  let index = 1;
  while (true) {
    const candidate = parent ? `${parent}/class-${index}` : `class-${index}`;
    if (!classes.some((item) => item.name === candidate)) {
      return candidate;
    }
    index++;
  }
}

function parentClass(name: string): string {
  const index = name.lastIndexOf("/");
  return index > 0 ? name.slice(0, index) : "";
}

function classSegment(name: string): string {
  const index = name.lastIndexOf("/");
  return index >= 0 ? name.slice(index + 1) : name;
}

function renameClass(classes: CgroupClass[], oldName: string, rawSegment: string): CgroupClass[] | null {
  const segment = rawSegment.trim();
  if (!segment || segment.includes("/")) {
    return null;
  }
  const parent = parentClass(oldName);
  const newName = parent ? `${parent}/${segment}` : segment;
  if (newName === oldName) {
    return classes;
  }
  if (classes.some((item) => item.name === newName)) {
    return null;
  }
  return classes.map((item) => {
    if (item.name === oldName) {
      return { ...item, name: newName };
    }
    if (item.name.startsWith(oldName + "/")) {
      return { ...item, name: newName + item.name.slice(oldName.length) };
    }
    return item;
  });
}

function classSummary(item: CgroupClass): string {
  const parts: string[] = [];
  if (item.cpus) {
    parts.push(`CPU ${item.cpus}`);
  }
  if (item.memory_max_bytes) {
    parts.push(`${bytesToMB(item.memory_max_bytes)} MB`);
  }
  if (item.cpu_weight) {
    parts.push(`weight ${item.cpu_weight}`);
  }
  if (item.io_limits?.length) {
    parts.push(`${item.io_limits.length} I/O`);
  }
  return parts.length ? parts.join(" · ") : "inherits host limits";
}

type CgroupClassEditorProps = {
  classes: CgroupClass[];
  devices: HostBlockDevice[];
  onlineCPUs: number[];
  disabled: boolean;
  onChange: (classes: CgroupClass[]) => void;
};

function CgroupClassEditor({ classes, devices, onlineCPUs, disabled, onChange }: CgroupClassEditorProps) {
  const update = (name: string, patch: Partial<CgroupClass>) => {
    onChange(classes.map((item) => (item.name === name ? { ...item, ...patch } : item)));
  };
  const remove = (name: string) => {
    onChange(classes.filter((item) => item.name !== name && !item.name.startsWith(name + "/")));
  };
  const add = (parent?: string) => {
    onChange([...classes, { name: nextClassName(classes, parent) }]);
  };

  const byName = new Map(classes.map((item) => [item.name, item]));
  const children = new Map<string, CgroupClass[]>();
  const roots: CgroupClass[] = [];
  for (const item of classes) {
    const parent = parentClass(item.name);
    if (parent && byName.has(parent)) {
      const current = children.get(parent) || [];
      current.push(item);
      children.set(parent, current);
    } else {
      roots.push(item);
    }
  }
  for (const list of children.values()) {
    list.sort((a, b) => a.name.localeCompare(b.name));
  }
  roots.sort((a, b) => a.name.localeCompare(b.name));

  const renderNode = (item: CgroupClass) => {
    const depth = item.name.split("/").length;
    const childItems = children.get(item.name) || [];
    const segment = classSegment(item.name);
    return (
      <div class="cgroup-tree-node" key={item.name}>
        <div class="cgroup-tree-node-head">
          <div class="cgroup-tree-title">
            <input
              class="cgroup-tree-name"
              type="text"
              defaultValue={segment}
              disabled={disabled}
              aria-label={`Class name for ${item.name}`}
              onBlur={(event) => {
                const renamed = renameClass(classes, item.name, event.currentTarget.value);
                if (!renamed) {
                  event.currentTarget.value = segment;
                  return;
                }
                onChange(renamed);
              }}
            />
            <span class="cgroup-tree-path">{item.name}</span>
            <span class="field-hint">{classSummary(item)}</span>
          </div>
          <div class="actions">
            {depth < 3 ? (
              <button class="row-action" type="button" disabled={disabled || classes.length >= 64} onClick={() => add(item.name)}>
                Add child
              </button>
            ) : null}
            <button
              class="row-action danger"
              type="button"
              disabled={disabled || childItems.length > 0}
              title={childItems.length > 0 ? "Remove child classes first." : ""}
              onClick={() => remove(item.name)}
            >
              Remove
            </button>
          </div>
        </div>

        <details class="cgroup-tree-limits">
          <summary>Limits</summary>
          <div class="form-grid cgroup-class-grid">
            <div class="field field-wide">
              <span class="field-label">CPU cores</span>
              <CPUSelector
                value={item.cpus || ""}
                options={onlineCPUs}
                disabled={disabled}
                onChange={(value) => update(item.name, { cpus: value })}
                hint="Empty inherits the host/parent class CPU set. A non-empty set must be a subset of it."
              />
            </div>

            <label class="field">
              <span class="field-label">Memory limit (MB)</span>
              <input
                type="number"
                min={0}
                step={128}
                disabled={disabled}
                value={bytesToMB(item.memory_max_bytes)}
                placeholder="inherit"
                onInput={(event) => update(item.name, { memory_max_bytes: mbToBytes(event.currentTarget.value) })}
              />
            </label>

            <label class="field">
              <span class="field-label">CPU weight</span>
              <input
                type="number"
                min={0}
                max={10000}
                step={1}
                disabled={disabled}
                value={weightValue(item.cpu_weight)}
                placeholder="inherit"
                onInput={(event) => update(item.name, { cpu_weight: limitNumber(event.currentTarget.value) })}
              />
            </label>

            <div class="field field-wide">
              <span class="field-label">Per-device I/O limits</span>
              <IOLimitEditor
                limits={item.io_limits || []}
                devices={devices}
                disabled={disabled}
                onChange={(limits) => update(item.name, { io_limits: limits })}
              />
            </div>
          </div>
        </details>

        {childItems.length > 0 ? <div class="cgroup-tree-children">{childItems.map(renderNode)}</div> : null}
      </div>
    );
  };

  return (
    <div class="cgroup-tree">
      <div class="cgroup-tree-root">
        <div>
          <span class="cgroup-tree-root-name">rookery-cgroup</span>
          <span class="field-hint">Root limits are the host settings above; classes nest up to 3 levels below.</span>
        </div>
        <button class="button ghost" type="button" disabled={disabled || classes.length >= 64} onClick={() => add()}>
          Add top-level class
        </button>
      </div>
      {roots.length > 0 ? roots.map(renderNode) : <p class="field-hint">No classes defined. VMs without a class run in the shared cgroup.</p>}
    </div>
  );
}
