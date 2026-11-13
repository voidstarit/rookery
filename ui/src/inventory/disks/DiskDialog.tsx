import { useEffect, useRef, useState } from "preact/hooks";
import { api } from "../../shared/api";
import { LabelsInput, saveEntityLabelsWithToast, useEntityLabelDraft } from "../../shared/labels";
import type { Disk, DiskThrottleClass, HostAttachableBlockDevice, StorageBackend } from "../types";
import { formatThrottleSummary } from "./throttle";

type DiskDialogProps = {
  backends: StorageBackend[];
  disks: Disk[];
  classes: DiskThrottleClass[];
  onClose: () => void;
  onSaved: (disk: Disk) => void;
};

const NAME_PATTERN = "[A-Za-z0-9][A-Za-z0-9._\\-]*";

export function DiskDialog({ backends, disks, classes, onClose, onSaved }: DiskDialogProps) {
  const defaultBackend = backends.find((item) => item.name === "default") || backends[0];
  const [storageKind, setStorageKind] = useState<"lvm" | "host">(backends.length === 0 ? "host" : "lvm");
  const [name, setName] = useState("");
  const [sizeGB, setSizeGB] = useState("10");
  const [throttleClassID, setThrottleClassID] = useState("0");
  const [backendID, setBackendID] = useState(defaultBackend ? String(defaultBackend.id) : "");
  const [hostDevices, setHostDevices] = useState<HostAttachableBlockDevice[]>([]);
  const [hostDevicePath, setHostDevicePath] = useState("");
  const [hostDevicesLoading, setHostDevicesLoading] = useState(false);
  const [hostDevicesError, setHostDevicesError] = useState("");
  const [source, setSource] = useState<"empty" | "template">("empty");
  const [templateID, setTemplateID] = useState("");
  const [markAsTemplate, setMarkAsTemplate] = useState(false);
  const [provisioningScript, setProvisioningScript] = useState("");
  const [provisioningTimeout, setProvisioningTimeout] = useState("5m");
  const [provisioningJSON, setProvisioningJSON] = useState("{}");
  const [labels, setLabels] = useEntityLabelDraft("disk");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const dialogRef = useRef<HTMLDialogElement | null>(null);
  const nameRef = useRef<HTMLInputElement | null>(null);
  const formRef = useRef<HTMLFormElement | null>(null);

  useEffect(() => {
    dialogRef.current?.showModal();
    nameRef.current?.focus();
  }, []);

  const backend = backends.find((item) => String(item.id) === backendID) || null;
  const supportsTemplates = Boolean(backend?.config?.supports_templates);
  const templates = disks.filter(
    (disk) => disk.is_template && String(disk.storage_backend_id ?? "") === backendID
  );
  const template = templates.find((disk) => String(disk.id) === templateID) || null;
  const hostDevice = hostDevices.find((device) => device.path === hostDevicePath) || null;
  const availableClasses = classes.filter((item) => !item.validation_error);
  const classDisabled = storageKind === "lvm" && source === "empty" && markAsTemplate;

  useEffect(() => {
    if (!supportsTemplates) {
      setSource("empty");
      setTemplateID("");
      setMarkAsTemplate(false);
    }
  }, [supportsTemplates]);

  useEffect(() => {
    if (storageKind !== "host") {
      return;
    }
    let cancelled = false;
    setHostDevicesLoading(true);
    setHostDevicesError("");
    api<HostAttachableBlockDevice[]>("/api/host/block-devices/attachable")
      .then((data) => {
        if (!cancelled) {
          setHostDevices(Array.isArray(data) ? data : []);
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setHostDevices([]);
          setHostDevicesError(err instanceof Error ? err.message : String(err));
        }
      })
      .finally(() => {
        if (!cancelled) {
          setHostDevicesLoading(false);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [storageKind]);

  const close = () => {
    if (!saving) {
      dialogRef.current?.close();
    }
  };

  const submit = async (event: Event) => {
    event.preventDefault();
    if (saving || !formRef.current?.reportValidity()) {
      return;
    }
    if (storageKind === "host" && (!hostDevice || !hostDevice.available)) {
      setError("Select an available host block device.");
      return;
    }
    let provisioning: unknown;
    if (storageKind === "lvm" && source === "template" && template?.provisioning_script) {
      try {
        provisioning = JSON.parse(provisioningJSON || "{}");
      } catch {
        setError("Provisioning properties must be valid JSON.");
        return;
      }
      if (provisioning === null || Array.isArray(provisioning) || typeof provisioning !== "object") {
        setError("Provisioning properties must be a JSON object.");
        return;
      }
    }
    setError("");
    setSaving(true);
    try {
      const classID = classDisabled ? 0 : Number(throttleClassID) || 0;
      const body =
        storageKind === "host"
          ? { name: name.trim(), host_device: hostDevice?.path, throttle_class_id: classID }
          : {
              name: name.trim(),
              size_gb: Number(sizeGB),
              storage_backend_id: Number(backendID),
              throttle_class_id: classID,
              ...(source === "template" && template ? { template_disk_id: template.id } : {}),
              ...(source === "template" && template?.provisioning_script && provisioning !== undefined
                ? { provisioning }
                : {}),
              ...(source === "empty" && supportsTemplates
                ? {
                    is_template: markAsTemplate,
                    ...(markAsTemplate
                      ? {
                          provisioning_script: provisioningScript.trim(),
                          provisioning_timeout: provisioningTimeout.trim(),
                        }
                      : {}),
                  }
                : {}),
            };
      const disk = await api<Disk>("/api/disks", {
        method: "POST",
        body: JSON.stringify(body),
      });
      await saveEntityLabelsWithToast("disk", disk.id, disk.name, labels);
      onSaved(disk);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  };

  return (
    <dialog
      ref={dialogRef}
      id="disk-dialog"
      aria-labelledby="disk-dialog-title"
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
            <p class="eyebrow">New disk</p>
            <h2 id="disk-dialog-title">Create disk</h2>
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
              maxlength={127}
              pattern={NAME_PATTERN}
              autocomplete="off"
              spellcheck={false}
              placeholder="vm01-root"
              value={name}
              onInput={(event) => setName(event.currentTarget.value)}
            />
            <span class="field-hint">Letters, digits, dots, dashes and underscores.</span>
          </label>

          <label class="field">
            <span class="field-label">Storage source</span>
            <select
              value={storageKind}
              onChange={(event) => setStorageKind(event.currentTarget.value as "lvm" | "host")}
            >
              <option value="lvm" disabled={backends.length === 0}>
                Provisioned storage (LVM thin or kouja)
              </option>
              <option value="host">Host block device (passthrough)</option>
            </select>
            <span class="field-hint">
              Provisioned disks use the selected storage backend. Passthrough disks hand an
              existing host disk, partition, or logical volume to one VM; Rookery never deletes
              or resizes the underlying device.
            </span>
          </label>

          <label class="field field-wide">
            <span class="field-label">Throttling class</span>
            <select
              value={throttleClassID}
              disabled={saving || classDisabled}
              onChange={(event) => setThrottleClassID(event.currentTarget.value)}
            >
              <option value="0">Unlimited</option>
              {availableClasses.map((item) => (
                <option value={String(item.id)} key={item.id}>
                  {item.name}
                  {formatThrottleSummary(item.limits) ? ` · ${formatThrottleSummary(item.limits)}` : ""}
                </option>
              ))}
            </select>
            <span class="field-hint">
              QEMU block throttling applies when the VM starts. Templates cannot carry a class.
            </span>
          </label>
          {storageKind === "lvm" ? (
            <>
              <label class="field">
                <span class="field-label">Size (GB)</span>
                <input
                  type="number"
                  required
                  min={template ? template.size_gb : 1}
                  max={65536}
                  step={1}
                  value={sizeGB}
                  onInput={(event) => setSizeGB(event.currentTarget.value)}
                />
                <span class="field-hint">Creates an LVM thin volume of this size in the configured pool.</span>
              </label>

              <label class="field">
                <span class="field-label">Storage backend</span>
                <select
                  required
                  value={backendID}
                  onChange={(event) => {
                    setBackendID(event.currentTarget.value);
                    setTemplateID("");
                    setSource("empty");
                  }}
                >
                  {backends.map((backend) => (
                    <option value={String(backend.id)} key={backend.id}>
                      {backend.name} · {backend.driver === "lvm-thin" ? "LVM thin" : backend.driver === "kouja" ? "Kouja" : backend.driver}
                    </option>
                  ))}
                </select>
                <span class="field-hint">
                  {backend?.driver === "kouja"
                    ? "Kouja provisions a remote volume and exports it over the selected protocol."
                    : supportsTemplates
                      ? "Template cloning is available on this backend."
                      : "Template cloning is disabled for this backend; enable it in Storage backends."}
                </span>
              </label>
              {supportsTemplates ? (
                <label class="field">
                  <span class="field-label">Creation source</span>
                  <select value={source} onChange={(event) => setSource(event.currentTarget.value as "empty" | "template")}>
                    <option value="empty">Empty disk</option>
                    <option value="template" disabled={templates.length === 0}>
                      From template{templates.length === 0 ? " (none available)" : ""}
                    </option>
                  </select>
                  <span class="field-hint">
                    Template clones run the backend preparation script and the template's
                    provisioning script when one is configured; empty disks use the thin pool directly.
                  </span>
                </label>
              ) : null}
              {supportsTemplates && source === "template" ? (
                <label class="field">
                  <span class="field-label">Template</span>
                  <select
                    required
                    value={templateID}
                    onChange={(event) => {
                      const value = event.currentTarget.value;
                      setTemplateID(value);
                      const selected = templates.find((disk) => String(disk.id) === value);
                      if (selected) {
                        setSizeGB(String(selected.size_gb));
                      }
                    }}
                  >
                    <option value="">Select a template…</option>
                    {templates.map((disk) => (
                      <option value={String(disk.id)} key={disk.id}>
                        {disk.name} · {disk.size_gb} GB{disk.provisioning_script ? " · provisioning" : ""}
                      </option>
                    ))}
                  </select>
                </label>
              ) : null}
              {supportsTemplates && source === "empty" ? (
                <label class="field field-wide checkbox-field">
                  <span class="checkbox-row">
                    <input
                      type="checkbox"
                      checked={markAsTemplate}
                      onChange={(event) => {
                        const checked = event.currentTarget.checked;
                        setMarkAsTemplate(checked);
                        if (checked) {
                          setThrottleClassID("0");
                        }
                      }}
                    />
                    <span>Mark this new disk as a template</span>
                  </span>
                </label>
              ) : null}
              {supportsTemplates && source === "empty" && markAsTemplate ? (
                <>
                  <label class="field field-wide">
                    <span class="field-label">VM provisioning script</span>
                    <input
                      type="text"
                      placeholder="/usr/local/sbin/rookery-provision-template"
                      value={provisioningScript}
                      onInput={(event) => setProvisioningScript(event.currentTarget.value)}
                    />
                    <span class="field-hint">
                      Optional absolute-path script run after a clone is created. It receives
                      <code>ROOKERY_PROVISION_JSON</code>.
                    </span>
                  </label>
                  <label class="field">
                    <span class="field-label">Provisioning timeout</span>
                    <input
                      type="text"
                      placeholder="5m"
                      value={provisioningTimeout}
                      onInput={(event) => setProvisioningTimeout(event.currentTarget.value)}
                    />
                  </label>
                </>
              ) : null}
              {supportsTemplates && source === "template" && template && !template.provisioning_script ? (
                <p class="field-hint field-wide">
                  This template has no provisioning script, so there are no key-values to pass.
                  Close this dialog and use <strong>Provisioning</strong> on the template row to
                  configure one first.
                </p>
              ) : null}
              {supportsTemplates && source === "template" && template?.provisioning_script ? (
                <label class="field field-wide">
                  <span class="field-label">Provisioning properties (JSON object)</span>
                  <textarea
                    rows={6}
                    spellcheck={false}
                    value={provisioningJSON}
                    onInput={(event) => setProvisioningJSON(event.currentTarget.value)}
                  />
                  <span class="field-hint">
                    Passed to the template provisioning script through
                    <code>ROOKERY_PROVISION_JSON</code> under <code>properties</code>.
                    Set <code>"resize_disk": true</code> to extend the root
                    partition and filesystem with the reference script. Leave
                    <code>{}</code> empty to skip provisioning.
                  </span>
                </label>
              ) : null}
            </>
          ) : (
            <>
              <label class="field field-wide">
                <span class="field-label">Host block device</span>
                <select
                  required
                  value={hostDevicePath}
                  disabled={hostDevicesLoading}
                  onChange={(event) => {
                    const value = event.currentTarget.value;
                    setHostDevicePath(value);
                    const selected = hostDevices.find((device) => device.path === value);
                    if (selected && !name.trim()) {
                      setName(defaultHostDiskName(selected));
                    }
                  }}
                >
                  <option value="">
                    {hostDevicesLoading ? "Loading devices…" : "Select a host block device…"}
                  </option>
                  {hostDevices.map((device) => (
                    <option
                      value={device.path}
                      key={`${device.kind}:${device.path}`}
                      disabled={!device.available}
                    >
                      {deviceKindLabel(device.kind)} · {device.path} · {formatDeviceBytes(device.size_bytes)}
                      {device.available ? "" : ` · ${device.unavailable_reason || "unavailable"}`}
                    </option>
                  ))}
                </select>
                <span class="field-hint">
                  Whole disks, partitions, logical volumes, RAID arrays, and loop/ram/zram
                  devices are listed. Mounted, swap, and otherwise busy devices are disabled.
                </span>
              </label>
              {hostDevicesError ? (
                <p class="form-error field-wide" role="alert">
                  {hostDevicesError}
                </p>
              ) : null}
              {hostDevice ? (
                <div class="field field-wide">
                  <span class="field-label">Selected device</span>
                  <p class="field-hint">
                    {deviceKindLabel(hostDevice.kind)} · {hostDevice.path} ·{" "}
                    {formatDeviceBytes(hostDevice.size_bytes)}
                    {hostDevice.model ? ` · ${hostDevice.model}` : ""}
                  </p>
                  {hostDevice.available ? null : (
                    <p class="form-error" role="alert">
                      {hostDevice.unavailable_reason || "This device is not available."}
                    </p>
                  )}
                </div>
              ) : null}
              {hostDevices.length === 0 && !hostDevicesLoading && !hostDevicesError ? (
                <p class="field-hint field-wide">No host block devices were found.</p>
              ) : null}
            </>
          )}
          <div class="field field-wide">
            <LabelsInput labels={labels} onChange={setLabels} disabled={saving} />
          </div>
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
          <button
            class="button primary"
            type="submit"
            disabled={
              saving ||
              (storageKind === "lvm"
                ? backends.length === 0 || (source === "template" && !template)
                : !hostDevice || !hostDevice.available)
            }
          >
            {saving ? "Creating…" : "Create disk"}
          </button>
        </footer>
      </form>
    </dialog>
  );
}

function deviceKindLabel(kind: string | undefined): string {
  switch (kind) {
    case "disk":
      return "Disk";
    case "partition":
      return "Partition";
    case "lv":
      return "Logical volume";
    case "raid":
      return "RAID array";
    case "loop":
      return "Loop device";
    case "ram":
      return "RAM disk";
    case "zram":
      return "ZRAM device";
    default:
      return "Block device";
  }
}

function formatDeviceBytes(bytes: number | undefined): string {
  const value = Number(bytes) || 0;
  if (value >= 1024 ** 4) {
    return `${(value / 1024 ** 4).toFixed(value % 1024 ** 4 === 0 ? 0 : 1)} TB`;
  }
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

function defaultHostDiskName(device: HostAttachableBlockDevice): string {
  const base = device.path.split("/").filter(Boolean).pop() || device.name;
  const cleaned = base
    .replace(/[^A-Za-z0-9._-]+/g, "-")
    .replace(/^[^A-Za-z0-9]+/, "")
    .slice(0, 127);
  return cleaned || device.name;
}
