import { useEffect, useRef, useState } from "preact/hooks";
import { api } from "../../shared/api";
import type {
  CgroupIOLimit,
  CgroupSettings,
  Disk,
  HostBlockDevice,
  Media,
  VM,
  VMDisk,
  VirtualInterface,
  VMInterface,
} from "../types";
import { CPUSelector, IOLimitEditor, parseCPUSet } from "../cgroup";

type VMDialogProps = {
  vm: VM | null;
  disks: Disk[];
  media: Media[];
  interfaces: VirtualInterface[];
  ready: boolean;
  onClose: () => void;
  onSaved: (vm: VM) => void;
};

const NAME_PATTERN = "[A-Za-z0-9][A-Za-z0-9._\\-]*";
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

function vmDisks(vm: VM | null): VMDisk[] {
  if (!vm) {
    return [];
  }
  if (Array.isArray(vm.disks) && vm.disks.length > 0) {
    return vm.disks;
  }
  if (vm.disk) {
    return [vm.disk];
  }
  if (vm.disk_id !== undefined) {
    return [];
  }
  return [];
}

function vmInterfaces(vm: VM | null): VMInterface[] {
  return Array.isArray(vm?.interfaces) ? vm.interfaces : [];
}

export function VMDialog({
  vm,
  disks,
  media,
  interfaces,
  ready,
  onClose,
  onSaved,
}: VMDialogProps) {
  const editing = vm !== null;
  const [name, setName] = useState(vm?.name ?? "");
  const [description, setDescription] = useState(vm?.description ?? "");
  const status = vm?.status ?? "stopped";
  const [vcpus, setVCPUs] = useState(String(vm?.vcpus ?? 1));
  const [memoryMB, setMemoryMB] = useState(String(vm?.memory_mb ?? 512));
  const [consoleEnabled, setConsoleEnabled] = useState(Boolean(vm?.console_enabled));
  const [cgroupSettings, setCgroupSettings] = useState<CgroupSettings | null>(null);
  const [cgroupEnabled, setCgroupEnabled] = useState(Boolean(vm?.cgroup?.enabled));
  const [cgroupCPUs, setCgroupCPUs] = useState(vm?.cgroup?.cpus || "");
  const [cgroupMemoryMB, setCgroupMemoryMB] = useState(bytesToMB(vm?.cgroup?.memory_max_bytes));
  const [cgroupCPUWeight, setCgroupCPUWeight] = useState(weightValue(vm?.cgroup?.cpu_weight));
  const [cgroupIOLimits, setCgroupIOLimits] = useState<CgroupIOLimit[]>(
    Array.isArray(vm?.cgroup?.io_limits) ? vm?.cgroup?.io_limits ?? [] : []
  );
  const [bootMediaID, setBootMediaID] = useState(
    vm?.boot_media_id !== undefined ? String(vm.boot_media_id) : ""
  );
  const [selectedDisks, setSelectedDisks] = useState<VMDisk[]>(() => vmDisks(vm));
  const [selectedInterfaces, setSelectedInterfaces] = useState<VMInterface[]>(() => vmInterfaces(vm));
  const [diskChoice, setDiskChoice] = useState("");
  const [interfaceChoice, setInterfaceChoice] = useState("");
  const [diskHint, setDiskHint] = useState("");
  const [interfaceHint, setInterfaceHint] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const dialogRef = useRef<HTMLDialogElement | null>(null);
  const nameRef = useRef<HTMLInputElement | null>(null);
  const formRef = useRef<HTMLFormElement | null>(null);

  const running = Boolean(vm && (vm.status === "running" || vm.status === "starting"));
  const selectedDiskIDs = new Set(selectedDisks.map((disk) => disk.id));
  const selectedInterfaceIDs = new Set(selectedInterfaces.map((iface) => iface.id));
  const cgroupDevices: HostBlockDevice[] = selectedDisks.map((disk) => ({
    name: disk.name,
    path: disk.device,
    model: `${disk.name} · ${disk.size_gb} GB`,
  }));
  const onlineCPUs = Array.isArray(cgroupSettings?.online_cpus) ? cgroupSettings.online_cpus : [];
  const hostCPUList = parseCPUSet(cgroupSettings?.cpus);
  const allowedCPUs = hostCPUList.length > 0 ? onlineCPUs.filter((cpu) => hostCPUList.includes(cpu)) : onlineCPUs;

  const availableDisks = disks.filter((disk) => {
    if (disk.is_template) {
      return false;
    }
    if (selectedDiskIDs.has(disk.id)) {
      return false;
    }
    return !disk.attached_vm_id || (vm !== null && disk.attached_vm_id === vm.id);
  });

  const availableInterfaces = interfaces.filter((iface) => {
    if (selectedInterfaceIDs.has(iface.id)) {
      return false;
    }
    return !iface.attached_vm_id || (vm !== null && iface.attached_vm_id === vm.id);
  });

  useEffect(() => {
    dialogRef.current?.showModal();
    nameRef.current?.focus();
  }, []);

  useEffect(() => {
    api<CgroupSettings>("/api/cgroup-settings")
      .then(setCgroupSettings)
      .catch(() => setCgroupSettings(null));
  }, []);
  useEffect(() => {
    if (running) {
      setDiskHint("Stop the VM before changing its disks.");
    } else if (availableDisks.length === 0 && selectedDisks.length === 0) {
      setDiskHint("Create a disk on the Disks tab first.");
    } else {
      setDiskHint("Choose a disk and click Add. The first disk is the boot disk. Remove disks with the buttons below.");
    }
  }, [availableDisks.length, selectedDisks.length, running]);

  useEffect(() => {
    if (running) {
      setInterfaceHint("Stop the VM before changing network interfaces.");
    } else if (availableInterfaces.length === 0 && selectedInterfaces.length === 0) {
      setInterfaceHint("Create a Rookery Virtual Interface on the Network tab first.");
    } else {
      setInterfaceHint("Choose an interface and click Add. Remove interfaces with the buttons below.");
    }
  }, [availableInterfaces.length, selectedInterfaces.length, running]);

  const close = () => {
    if (!saving) {
      dialogRef.current?.close();
    }
  };

  const addDisk = () => {
    const id = Number(diskChoice);
    if (!id || running) {
      return;
    }
    const disk = disks.find((item) => item.id === id);
    const selected = disk ?? selectedDisks.find((item) => item.id === id);
    if (selected) {
      setSelectedDisks((current) => (current.some((item) => item.id === selected.id) ? current : [...current, selected]));
      setDiskChoice("");
    }
  };

  const removeDisk = (id: number) => {
    if (running) {
      return;
    }
    setSelectedDisks((current) => current.filter((disk) => disk.id !== id));
  };

  const addInterface = () => {
    const id = Number(interfaceChoice);
    if (!id || running) {
      return;
    }
    const iface =
      interfaces.find((item) => item.id === id) ||
      selectedInterfaces.find((item) => item.id === id);
    if (iface) {
      setSelectedInterfaces((current) =>
        current.some((item) => item.id === iface.id) ? current : [...current, iface]
      );
      setInterfaceChoice("");
    }
  };

  const removeInterface = (id: number) => {
    if (running) {
      return;
    }
    setSelectedInterfaces((current) => current.filter((iface) => iface.id !== id));
  };

  const submit = async (event: Event) => {
    event.preventDefault();
    if (saving) {
      return;
    }
    if (!editing && !ready) {
      setError("VM creation is blocked until the thin pool and FD.io VPP are ready.");
      return;
    }
    if (!formRef.current?.reportValidity()) {
      return;
    }
    if (selectedDisks.length === 0) {
      setError("Select at least one disk.");
      return;
    }

    const payload: Record<string, unknown> = {
      name: name.trim(),
      description: description.trim(),
      vcpus: Number(vcpus),
      memory_mb: Number(memoryMB),
      console_enabled: consoleEnabled,
    };

    if (!editing) {
      payload.disk_ids = selectedDisks.map((disk) => disk.id);
      if (selectedInterfaces.length > 0) {
        payload.interface_ids = selectedInterfaces.map((iface) => iface.id);
      }
    } else if (!running) {
      payload.disk_ids = selectedDisks.map((disk) => disk.id);
      payload.interface_ids = selectedInterfaces.map((iface) => iface.id);
      // The gateway treats omitted repeated fields as unchanged; editing the
      // dialog is an explicit replacement, including an empty interface set.
      payload.replace_disk_ids = true;
      payload.replace_interface_ids = true;
    }

    const bootMedia = Number(bootMediaID);
    if (bootMedia) {
      payload.boot_media_id = bootMedia;
    }

    payload.cgroup = {
      enabled: cgroupEnabled,
      cpus: cgroupCPUs.trim(),
      memory_max_bytes: mbToBytes(cgroupMemoryMB),
      cpu_weight: limitNumber(cgroupCPUWeight),
      io_limits: cgroupIOLimits.map((limit) => ({
        device: limit.device,
        read_bps: limit.read_bps || 0,
        write_bps: limit.write_bps || 0,
        read_iops: limit.read_iops || 0,
        write_iops: limit.write_iops || 0,
        weight: limit.weight || 0,
      })),
    };

    setError("");
    setSaving(true);
    try {
      const saved = await api<VM>(editing ? `/api/vms/${vm?.id}` : "/api/vms", {
        method: editing ? "PUT" : "POST",
        body: JSON.stringify(payload),
      });
      onSaved(saved);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  };

  return (
    <dialog
      ref={dialogRef}
      id="vm-dialog"
      aria-labelledby="dialog-title"
      onClose={onClose}
      onClick={(event) => {
        if (event.target === event.currentTarget) {
          close();
        }
      }}
      onCancel={(event) => {
        if (saving) {
          event.preventDefault();
        }
      }}
    >
      <form ref={formRef} novalidate onSubmit={(event) => void submit(event)}>
        <header class="dialog-head">
          <div>
            <p class="eyebrow">{editing ? "Edit instance" : "New instance"}</p>
            <h2 id="dialog-title">{editing ? `Edit ${vm?.name}` : "Create VM"}</h2>
          </div>
          <button class="icon-button" type="button" aria-label="Close dialog" onClick={close}>
            ×
          </button>
        </header>

        <div class="form-grid">
          <label class="field field-wide">
            <span class="field-label">Name</span>
            <input
              ref={nameRef}
              type="text"
              required
              maxlength={64}
              pattern={NAME_PATTERN}
              autocomplete="off"
              spellcheck={false}
              placeholder="web-01"
              value={name}
              onInput={(event) => setName(event.currentTarget.value)}
            />
            <span class="field-hint">Letters, digits, dots, dashes and underscores.</span>
          </label>

          <label class="field">
            <span class="field-label">Power</span>
            <input type="text" value={editing ? status : "Starts automatically"} disabled />
            <span class="field-hint">
              {editing
                ? "Managed by Start/Stop on the inventory table."
                : "New VMs start right away; use Stop on the inventory table to shut one down."}
            </span>
          </label>

          <label class="field">
            <span class="field-label">vCPUs</span>
            <input
              type="number"
              required
              min={1}
              max={128}
              step={1}
              value={vcpus}
              onInput={(event) => setVCPUs(event.currentTarget.value)}
            />
          </label>

          <label class="field">
            <span class="field-label">Memory (MB)</span>
            <input
              type="number"
              required
              min={128}
              max={1048576}
              step={128}
              value={memoryMB}
              onInput={(event) => setMemoryMB(event.currentTarget.value)}
            />
          </label>

          <div class="field field-wide">
            <span class="field-label">Disks</span>
            <div class="interface-picker">
              <select
                value={diskChoice}
                disabled={running || availableDisks.length === 0}
                onChange={(event) => setDiskChoice(event.currentTarget.value)}
              >
                <option value="">{availableDisks.length > 0 ? "Select a disk…" : "No disks available"}</option>
                {availableDisks.map((disk) => (
                  <option value={String(disk.id)} key={disk.id}>
                    {disk.name} · {disk.size_gb} GB
                  </option>
                ))}
              </select>
              <button
                class="button ghost"
                type="button"
                disabled={running || availableDisks.length === 0}
                onClick={addDisk}
              >
                Add
              </button>
            </div>
            <ul class="interface-list">
              {selectedDisks.length === 0 ? (
                <li class="empty">No disks selected</li>
              ) : (
                selectedDisks.map((disk, index) => (
                  <li key={disk.id}>
                    <span class="interface-list-name">
                      {disk.name}
                      <span class="interface-list-network">
                        {disk.size_gb} GB{index === 0 ? " · boot" : ""}
                      </span>
                    </span>
                    <button
                      class="row-action danger"
                      type="button"
                      disabled={running}
                      onClick={() => removeDisk(disk.id)}
                    >
                      Remove
                    </button>
                  </li>
                ))
              )}
            </ul>
            <span class="field-hint">{diskHint}</span>
          </div>

          <label class="field field-wide">
            <span class="field-label">Boot media <em>optional</em></span>
            <select value={bootMediaID} onChange={(event) => setBootMediaID(event.currentTarget.value)}>
              <option value="">Boot from disk</option>
              {media.map((item) => (
                <option value={String(item.id)} key={item.id}>
                  {item.name} · {formatBytes(item.size_bytes)}
                </option>
              ))}
            </select>
            <span class="field-hint">Read-only ISO from the Installation media tab. You can change this later.</span>
          </label>

          <label class="field field-wide checkbox-field">
            <span class="field-label">Console access</span>
            <span class="checkbox-row">
              <input
                type="checkbox"
                checked={consoleEnabled}
                onChange={(event) => setConsoleEnabled(event.currentTarget.checked)}
              />
              <span>Allow end users to open this VM's console</span>
            </span>
            <span class="field-hint">When disabled, the console page and VNC WebSocket are blocked.</span>
          </label>

          <div class="field field-wide cgroup-vm-section">
            <span class="field-label">
              Resource limits <em>optional</em>
            </span>
            <label class="checkbox-field">
              <span class="checkbox-row">
                <input
                  type="checkbox"
                  checked={cgroupEnabled}
                  onChange={(event) => setCgroupEnabled(event.currentTarget.checked)}
                />
                <span>Run this VM in its own cgroup child</span>
              </span>
            </label>
            <span class="field-hint">
              Leave disabled to run under the shared host cgroup. Changes are applied immediately:
              Rookery moves a running QEMU process into or out of this VM's child cgroup. The
              host-wide limits in Settings are always inherited.
              {cgroupSettings && !cgroupSettings.enabled
                ? " Host-wide cgroup limits are disabled in Settings; per-VM limits apply when host cgroup limits are enabled."
                : ""}
            </span>

            <div class="form-grid cgroup-vm-fields">
              <div class="field field-wide">
                <span class="field-label">CPU cores</span>
                <CPUSelector
                  value={cgroupCPUs}
                  options={allowedCPUs}
                  disabled={!cgroupEnabled}
                  onChange={setCgroupCPUs}
                  hint="Empty inherits the host-wide CPU set. Only CPUs allowed by Settings can be selected."
                />
              </div>

              <label class="field">
                <span class="field-label">Memory limit (MB)</span>
                <input
                  type="number"
                  min={0}
                  step={128}
                  disabled={!cgroupEnabled}
                  value={cgroupMemoryMB}
                  placeholder="inherit host"
                  onInput={(event) => setCgroupMemoryMB(event.currentTarget.value)}
                />
                <span class="field-hint">Must be at least the VM memory when set; allow headroom for QEMU overhead.</span>
              </label>

              <label class="field">
                <span class="field-label">CPU weight</span>
                <input
                  type="number"
                  min={0}
                  max={10000}
                  step={1}
                  disabled={!cgroupEnabled}
                  value={cgroupCPUWeight}
                  placeholder="inherit host"
                  onInput={(event) => setCgroupCPUWeight(event.currentTarget.value)}
                />
                <span class="field-hint">Leave empty to inherit the host CPU weight.</span>
              </label>

              <div class="field field-wide">
                <span class="field-label">Per-disk I/O limits</span>
                <IOLimitEditor
                  limits={cgroupIOLimits}
                  devices={cgroupDevices}
                  disabled={!cgroupEnabled}
                  onChange={setCgroupIOLimits}
                />
                <span class="field-hint">
                  Values override the host per-device limits for this VM. Empty values inherit the
                  host limit.
                </span>
              </div>
            </div>
          </div>

          <div class="field field-wide">
            <span class="field-label">Network interfaces <em>optional</em></span>
            <div class="interface-picker">
              <select
                value={interfaceChoice}
                disabled={running || availableInterfaces.length === 0}
                onChange={(event) => setInterfaceChoice(event.currentTarget.value)}
              >
                <option value="">
                  {availableInterfaces.length > 0 ? "Select an interface…" : "No interfaces available"}
                </option>
                {availableInterfaces.map((iface) => (
                  <option value={String(iface.id)} key={iface.id}>
                    {iface.name} · {iface.network_name}
                  </option>
                ))}
              </select>
              <button
                class="button ghost"
                type="button"
                disabled={running || availableInterfaces.length === 0}
                onClick={addInterface}
              >
                Add
              </button>
            </div>
            <ul class="interface-list">
              {selectedInterfaces.length === 0 ? (
                <li class="empty">No interfaces selected</li>
              ) : (
                selectedInterfaces.map((iface) => (
                  <li key={iface.id}>
                    <span class="interface-list-name">
                      {iface.name}
                      <span class="interface-list-network">{iface.network_name}</span>
                    </span>
                    <button
                      class="row-action danger"
                      type="button"
                      disabled={running}
                      onClick={() => removeInterface(iface.id)}
                    >
                      Remove
                    </button>
                  </li>
                ))
              )}
            </ul>
            <span class="field-hint">{interfaceHint}</span>
          </div>

          <label class="field field-wide">
            <span class="field-label">Description <em>optional</em></span>
            <textarea
              rows={3}
              maxlength={2000}
              placeholder="What is this instance for?"
              value={description}
              onInput={(event) => setDescription(event.currentTarget.value)}
            />
          </label>
        </div>

        {error ? (
          <p class="form-error" role="alert">
            {error}
          </p>
        ) : null}

        <footer class="dialog-actions">
          <button class="button ghost" type="button" disabled={saving} onClick={close}>
            Cancel
          </button>
          <button class="button primary" type="submit" disabled={saving}>
            {saving ? (editing ? "Saving…" : "Creating…") : editing ? "Save changes" : "Create VM"}
          </button>
        </footer>
      </form>
    </dialog>
  );
}

function formatBytes(bytes: number): string {
  const value = Number(bytes) || 0;
  if (value >= 1024 ** 3) {
    return `${(value / 1024 ** 3).toFixed(value % 1024 ** 3 === 0 ? 0 : 1)} GB`;
  }
  if (value >= 1024 ** 2) {
    return `${(value / 1024 ** 2).toFixed(value % 1024 ** 2 === 0 ? 0 : 1)} MB`;
  }
  if (value >= 1024) {
    return `${(value / 1024).toFixed(value % 1024 === 0 ? 0 : 1)} KB`;
  }
  return `${value} B`;
}
