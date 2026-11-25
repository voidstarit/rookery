import { useEffect, useRef, useState } from "preact/hooks";
import { api } from "../../shared/api";
import { showToast } from "../../shared/toast";
import type {
  LightVMEnvVar,
  LightVMInitramfs,
  LightVMKernel,
  LightVMTemplate,
  LightVMTemplateDisk,
  LightVMTemplateInterface,
  LightVMTemplateSpec,
  LightVMTemplateVersion,
  Network,
  StorageBackend,
} from "../types";

type Mode = "create" | "version" | "metadata";

type Props = {
  mode: Mode;
  template: LightVMTemplate | null;
  kernels: LightVMKernel[];
  initramfs: LightVMInitramfs[];
  backends: StorageBackend[];
  networks: Network[];
  onClose: () => void;
  onSaved: () => void;
};

const DEFAULT_APPEND = "console=hvc0 rdinit=/init";

function parseEnv(text: string): LightVMEnvVar[] {
  const values: LightVMEnvVar[] = [];
  for (const line of text.split("\n")) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("#")) {
      continue;
    }
    const separator = trimmed.indexOf("=");
    if (separator < 1) {
      continue;
    }
    values.push({
      name: trimmed.slice(0, separator).trim(),
      value: trimmed.slice(separator + 1),
    });
  }
  return values;
}

function envText(values: LightVMEnvVar[] | undefined): string {
  return (values ?? []).map((item) => `${item.name}=${item.value}`).join("\n");
}

export function TemplateEditorDialog({
  mode,
  template,
  kernels,
  initramfs,
  backends,
  networks,
  onClose,
  onSaved,
}: Props) {
  const dialogRef = useRef<HTMLDialogElement | null>(null);
  const [name, setName] = useState(template?.name ?? "");
  const [description, setDescription] = useState(template?.description ?? "");
  const [vcpus, setVCPUs] = useState("2");
  const [memoryMB, setMemoryMB] = useState("1024");
  const [cgroupClass, setCgroupClass] = useState("");
  const [kernelID, setKernelID] = useState("");
  const [initramfsID, setInitramfsID] = useState("");
  const [image, setImage] = useState("");
  const [append, setAppend] = useState(DEFAULT_APPEND);
  const [consoleEnabled, setConsoleEnabled] = useState(true);
  const [executionMode, setExecutionMode] = useState<"path" | "script">("script");
  const [executablePath, setExecutablePath] = useState("");
  const [executableScript, setExecutableScript] = useState("#!/bin/sh\n");
  const [environment, setEnvironment] = useState("");
  const [disks, setDisks] = useState<LightVMTemplateDisk[]>([]);
  const [interfaces, setInterfaces] = useState<LightVMTemplateInterface[]>([]);
  const [loading, setLoading] = useState(mode === "version");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    dialogRef.current?.showModal();
  }, []);

  useEffect(() => {
    if (mode !== "version" || !template) {
      return;
    }
    void (async () => {
      try {
        const version = await api<LightVMTemplateVersion>(
          `/api/lightvm/templates/${template.id}/versions/${template.latest_version_id}`
        );
        const spec = version.spec;
        setVCPUs(String(spec.vcpus));
        setMemoryMB(String(spec.memory_mb));
        setCgroupClass(spec.cgroup_class ?? "");
        setKernelID(String(spec.kernel_id));
        setInitramfsID(String(spec.initramfs_id));
        setImage(spec.image);
        setAppend(spec.append || DEFAULT_APPEND);
        setConsoleEnabled(Boolean(spec.console_enabled));
        setExecutionMode(spec.executable_script ? "script" : "path");
        setExecutablePath(spec.executable_path ?? "");
        setExecutableScript(spec.executable_script ?? "");
        setEnvironment(envText(spec.env));
        setDisks(spec.disks ?? []);
        setInterfaces(spec.interfaces ?? []);
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err));
      } finally {
        setLoading(false);
      }
    })();
  }, [mode, template]);

  const close = () => {
    if (!saving) {
      dialogRef.current?.close();
      onClose();
    }
  };

  const addDisk = () => {
    const backend = backends[0];
    if (!backend) {
      setError("Register a storage backend before adding a template disk.");
      return;
    }
    setDisks([...disks, { storage_backend_id: backend.id, size_gb: 10, serial: "" }]);
  };

  const addInterface = () => {
    const network = networks[0];
    if (!network) {
      setError("Create a Rookery Network before adding a template interface.");
      return;
    }
    setInterfaces([...interfaces, { network_id: network.id }]);
  };

  const submit = async (event: Event) => {
    event.preventDefault();
    setError("");
    if (mode === "metadata") {
      if (!template) {
        return;
      }
      setSaving(true);
      try {
        await api(`/api/lightvm/templates/${template.id}`, {
          method: "PUT",
          body: JSON.stringify({ name: name.trim(), description: description.trim() }),
        });
        showToast(`Updated template ${name.trim()}`);
        onSaved();
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err));
      } finally {
        setSaving(false);
      }
      return;
    }
    const spec: LightVMTemplateSpec = {
      vcpus: Number(vcpus),
      memory_mb: Number(memoryMB),
      cgroup_class: cgroupClass.trim(),
      kernel_id: Number(kernelID),
      initramfs_id: Number(initramfsID),
      image: image.trim(),
      env: parseEnv(environment),
      executable_path: executionMode === "path" ? executablePath.trim() : "",
      executable_script: executionMode === "script" ? executableScript : "",
      append: append.trim(),
      console_enabled: consoleEnabled,
      consoles: [{ id: 0, name: "console", port_name: "" }],
      disks,
      interfaces,
    };
    setSaving(true);
    try {
      if (mode === "create") {
        await api<LightVMTemplate>("/api/lightvm/templates", {
          method: "POST",
          body: JSON.stringify({ name: name.trim(), description: description.trim(), spec }),
        });
        showToast(`Created template ${name.trim()}`);
      } else if (template) {
        await api(`/api/lightvm/templates/${template.id}/versions`, {
          method: "POST",
          body: JSON.stringify({ spec }),
        });
        showToast(`Created a new version of ${template.name}`);
      }
      onSaved();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  };

  return (
    <dialog
      ref={dialogRef}
      id="lightvm-template-dialog"
      aria-labelledby="lightvm-template-dialog-title"
      onClose={onClose}
      onClick={(event) => {
        if (event.target === event.currentTarget) {
          close();
        }
      }}
    >
      <form onSubmit={(event) => void submit(event)}>
        <header class="dialog-head">
          <div>
            <p class="eyebrow">LightVM template</p>
            <h2 id="lightvm-template-dialog-title">
              {mode === "create"
                ? "Create template"
                : mode === "metadata"
                  ? `Edit ${template?.name}`
                  : `New version of ${template?.name}`}
            </h2>
          </div>
          <button class="icon-button" type="button" aria-label="Close dialog" onClick={close}>
            ×
          </button>
        </header>

        {error ? <div class="banner" role="alert">{error}</div> : null}
        {loading ? <p class="dialog-note">Loading the latest template specification…</p> : null}

        <fieldset class="form-grid dialog-form-fieldset" disabled={saving || loading}>
          <label class="field field-wide">
            <span class="field-label">Name</span>
            <input
              required
              maxlength={64}
              value={name}
              onInput={(event) => setName(event.currentTarget.value)}
            />
          </label>
          <label class="field field-wide">
            <span class="field-label">Description</span>
            <input
              maxlength={2000}
              value={description}
              onInput={(event) => setDescription(event.currentTarget.value)}
            />
          </label>

          {mode !== "metadata" ? (
            <>
              <label class="field">
                <span class="field-label">vCPUs</span>
                <input
                  type="number"
                  min={1}
                  max={128}
                  required
                  value={vcpus}
                  onInput={(event) => setVCPUs(event.currentTarget.value)}
                />
              </label>
              <label class="field">
                <span class="field-label">Memory (MB)</span>
                <input
                  type="number"
                  min={128}
                  max={1048576}
                  step={128}
                  required
                  value={memoryMB}
                  onInput={(event) => setMemoryMB(event.currentTarget.value)}
                />
              </label>
              <label class="field">
                <span class="field-label">Cgroup class</span>
                <input
                  value={cgroupClass}
                  placeholder="empty = shared"
                  onInput={(event) => setCgroupClass(event.currentTarget.value)}
                />
              </label>
              <label class="field">
                <span class="field-label">Kernel</span>
                <select required value={kernelID} onChange={(event) => setKernelID(event.currentTarget.value)}>
                  <option value="">Select a kernel…</option>
                  {kernels.map((item) => (
                    <option value={String(item.id)} key={item.id}>{item.name}</option>
                  ))}
                </select>
              </label>
              <label class="field">
                <span class="field-label">Initramfs</span>
                <select required value={initramfsID} onChange={(event) => setInitramfsID(event.currentTarget.value)}>
                  <option value="">Select an initramfs…</option>
                  {initramfs.map((item) => (
                    <option value={String(item.id)} key={item.id}>{item.name}</option>
                  ))}
                </select>
              </label>
              <label class="field field-wide">
                <span class="field-label">Container image</span>
                <input
                  required
                  value={image}
                  placeholder="registry/image@sha256:…"
                  onInput={(event) => setImage(event.currentTarget.value)}
                />
                <span class="field-hint">Use a digest-qualified image so every member is identical.</span>
              </label>
              <label class="field field-wide">
                <span class="field-label">Kernel append</span>
                <input value={append} onInput={(event) => setAppend(event.currentTarget.value)} />
              </label>
              <label class="field">
                <span class="field-label">Console</span>
                <label class="checkbox">
                  <input
                    type="checkbox"
                    checked={consoleEnabled}
                    onChange={(event) => setConsoleEnabled(event.currentTarget.checked)}
                  />
                  Enable console
                </label>
              </label>
              <label class="field">
                <span class="field-label">Executable mode</span>
                <select
                  value={executionMode}
                  onChange={(event) => setExecutionMode(event.currentTarget.value as "path" | "script")}
                >
                  <option value="script">Inline script</option>
                  <option value="path">Executable path</option>
                </select>
              </label>
              {executionMode === "path" ? (
                <label class="field field-wide">
                  <span class="field-label">Executable path</span>
                  <input value={executablePath} onInput={(event) => setExecutablePath(event.currentTarget.value)} />
                </label>
              ) : (
                <label class="field field-wide">
                  <span class="field-label">Executable script</span>
                  <textarea
                    rows={6}
                    value={executableScript}
                    onInput={(event) => setExecutableScript(event.currentTarget.value)}
                  />
                </label>
              )}
              <label class="field field-wide">
                <span class="field-label">Environment</span>
                <textarea
                  rows={4}
                  placeholder="NAME=value"
                  value={environment}
                  onInput={(event) => setEnvironment(event.currentTarget.value)}
                />
              </label>

              <div class="field field-wide">
                <div class="panel-head compact">
                  <div>
                    <span class="field-label">Data disks</span>
                    <p class="field-hint">Each member gets its own blank disk.</p>
                  </div>
                  <button class="button ghost" type="button" onClick={addDisk}>Add disk</button>
                </div>
                {disks.map((disk, index) => (
                  <div class="template-row" key={`${disk.storage_backend_id}-${index}`}>
                    <select
                      value={String(disk.storage_backend_id)}
                      onChange={(event) => {
                        const next = [...disks];
                        next[index] = { ...disk, storage_backend_id: Number(event.currentTarget.value) };
                        setDisks(next);
                      }}
                    >
                      {backends.map((backend) => (
                        <option value={String(backend.id)} key={backend.id}>{backend.name}</option>
                      ))}
                    </select>
                    <input
                      type="number"
                      min={1}
                      max={65536}
                      value={disk.size_gb}
                      onInput={(event) => {
                        const next = [...disks];
                        next[index] = { ...disk, size_gb: Number(event.currentTarget.value) };
                        setDisks(next);
                      }}
                    />
                    <input
                      placeholder="serial (optional)"
                      value={disk.serial ?? ""}
                      onInput={(event) => {
                        const next = [...disks];
                        next[index] = { ...disk, serial: event.currentTarget.value };
                        setDisks(next);
                      }}
                    />
                    <button
                      class="row-action danger"
                      type="button"
                      onClick={() => setDisks(disks.filter((_, item) => item !== index))}
                    >
                      Remove
                    </button>
                  </div>
                ))}
              </div>

              <div class="field field-wide">
                <div class="panel-head compact">
                  <div>
                    <span class="field-label">Interfaces</span>
                    <p class="field-hint">Each member gets its own vhost-user interface.</p>
                  </div>
                  <button class="button ghost" type="button" onClick={addInterface}>Add interface</button>
                </div>
                {interfaces.map((iface, index) => (
                  <div class="template-row" key={`${iface.network_id}-${index}`}>
                    <select
                      value={String(iface.network_id)}
                      onChange={(event) => {
                        const next = [...interfaces];
                        next[index] = { ...iface, network_id: Number(event.currentTarget.value) };
                        setInterfaces(next);
                      }}
                    >
                      {networks.map((network) => (
                        <option value={String(network.id)} key={network.id}>{network.name}</option>
                      ))}
                    </select>
                    <button
                      class="row-action danger"
                      type="button"
                      onClick={() => setInterfaces(interfaces.filter((_, item) => item !== index))}
                    >
                      Remove
                    </button>
                  </div>
                ))}
              </div>
            </>
          ) : null}
        </fieldset>

        <div class="dialog-actions">
          <button class="button ghost" type="button" disabled={saving} onClick={close}>
            Cancel
          </button>
          <button class="button primary" type="submit" disabled={saving || loading}>
            {saving ? "Saving…" : mode === "metadata" ? "Save changes" : mode === "version" ? "Create version" : "Create template"}
          </button>
        </div>
      </form>
    </dialog>
  );
}
