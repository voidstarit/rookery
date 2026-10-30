import { useEffect, useState } from "preact/hooks";
import { api } from "../../shared/api";
import { showToast } from "../../shared/toast";
import type { CgroupClass, CgroupIOLimit, CgroupSettings, HostBlockDevice } from "../types";
import { CPUSelector, IOLimitEditor } from "../cgroup";

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

  useEffect(() => {
    void load();
  }, []);

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
      ? `Applied at ${settings?.root || "/sys/fs/cgroup/rookery"}`
      : "Enabled but not applied yet"
    : "Disabled";

  return (
    <section class="panel panel-spaced" aria-labelledby="cgroup-settings-heading">
      <div class="panel-head">
        <div>
          <h2 id="cgroup-settings-heading">Cgroup v2 resources</h2>
          <p class="panel-subtitle">Host-wide ceilings inherited by every QEMU process</p>
        </div>
        <button class="button ghost" type="button" disabled={loading || saving} onClick={() => void load()}>
          {loading ? "Refreshing…" : "Refresh"}
        </button>
      </div>

      <p class="field-hint">
        Rookery runs QEMU below {settings?.root || "/sys/fs/cgroup/rookery"}. VMs without a cgroup
        class run in the shared child cgroup; each class gets a hierarchical child cgroup under
        <code>classes/</code>.
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
          <div class="cgroup-class-head">
            <span class="field-label">Classes</span>
            <button
              class="button ghost"
              type="button"
              disabled={loading || saving || classes.length >= 64}
              onClick={() => setClasses((current) => [...current, nextClass(current)])}
            >
              Add class
            </button>
          </div>
          <span class="field-hint">
            Define reusable profiles as slash-separated paths such as <code>prod</code>,{" "}
            <code>prod/web</code>, or <code>prod/web/frontend</code>. Parent cgroups are created
            automatically. Assign a class to a VM instead of entering limits per VM. Class limits are
            shared by every VM assigned to that class; allow enough memory for all of them.
          </span>
          <CgroupClassEditor
            classes={classes}
            devices={devices}
            onlineCPUs={onlineCPUs}
            disabled={loading}
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

function nextClass(classes: CgroupClass[]): CgroupClass {
  let index = 1;
  while (classes.some((item) => item.name === `class-${index}`)) {
    index++;
  }
  return { name: `class-${index}` };
}

type CgroupClassEditorProps = {
  classes: CgroupClass[];
  devices: HostBlockDevice[];
  onlineCPUs: number[];
  disabled: boolean;
  onChange: (classes: CgroupClass[]) => void;
};

function CgroupClassEditor({ classes, devices, onlineCPUs, disabled, onChange }: CgroupClassEditorProps) {
  const update = (index: number, patch: Partial<CgroupClass>) => {
    onChange(classes.map((item, current) => (current === index ? { ...item, ...patch } : item)));
  };
  const remove = (index: number) => {
    onChange(classes.filter((_, current) => current !== index));
  };

  if (classes.length === 0) {
    return <p class="field-hint">No classes defined. VMs without a class run in the shared cgroup.</p>;
  }

  return (
    <div class="cgroup-class-editor">
      {classes.map((item, index) => (
        <div class="cgroup-class" key={index}>
          <div class="cgroup-class-head">
            <label class="field cgroup-class-name">
              <span class="field-label">Class path</span>
              <input
                type="text"
                disabled={disabled}
                value={item.name || ""}
                placeholder="prod/web"
                spellcheck={false}
                onInput={(event) => update(index, { name: event.currentTarget.value })}
              />
            </label>
            <button class="row-action danger" type="button" disabled={disabled} onClick={() => remove(index)}>
              Remove class
            </button>
          </div>

          <div class="form-grid cgroup-class-grid">
            <div class="field field-wide">
              <span class="field-label">CPU cores</span>
              <CPUSelector
                value={item.cpus || ""}
                options={onlineCPUs}
                disabled={disabled}
                onChange={(value) => update(index, { cpus: value })}
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
                onInput={(event) => update(index, { memory_max_bytes: mbToBytes(event.currentTarget.value) })}
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
                onInput={(event) => update(index, { cpu_weight: limitNumber(event.currentTarget.value) })}
              />
            </label>

            <div class="field field-wide">
              <span class="field-label">Per-device I/O limits</span>
              <IOLimitEditor
                limits={item.io_limits || []}
                devices={devices}
                disabled={disabled}
                onChange={(limits) => update(index, { io_limits: limits })}
              />
            </div>
          </div>
        </div>
      ))}
    </div>
  );
}

