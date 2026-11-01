import { useEffect, useRef, useState } from "preact/hooks";
import { api } from "../../shared/api";
import { LabelsInput, saveEntityLabelsWithToast, useEntityLabelDraft } from "../../shared/labels";
import type {
  CgroupSettings,
  LightVM,
  LightVMEnvVar,
  LightVMInitramfs,
  LightVMKernel,
  VMInterface,
  VirtualInterface,
} from "../types";

type LightVMDialogProps = {
  vm: LightVM | null;
  kernels: LightVMKernel[];
  initramfs: LightVMInitramfs[];
  interfaces: VirtualInterface[];
  onClose: () => void;
  onSaved: (vm: LightVM) => void;
};

const NAME_PATTERN = "[A-Za-z0-9][A-Za-z0-9._\\-]*";
const ENV_NAME_PATTERN = /^[A-Za-z_][A-Za-z0-9_]*$/;
const DEFAULT_APPEND = "console=hvc0 rdinit=/init";

type ConsoleRow = { id: number; name: string };

function consoleRows(vm: LightVM | null): ConsoleRow[] {
  if (Array.isArray(vm?.consoles) && vm.consoles.length > 0) {
    return vm.consoles.map((console) => ({ id: console.id, name: console.name }));
  }
  return [{ id: 0, name: "console" }];
}

function nextConsoleName(current: ConsoleRow[]): string {
  if (current.length === 0) {
    return "console";
  }
  const used = new Set(current.map((console) => console.name.trim().toLowerCase()));
  let index = current.length + 1;
  while (used.has(`console-${index}`)) {
    index += 1;
  }
  return `console-${index}`;
}

function envText(vm: LightVM | null): string {
  if (!Array.isArray(vm?.env)) {
    return "";
  }
  return vm.env.map((item) => `${item.name}=${item.value}`).join("\n");
}

function parseEnvText(text: string): { env: LightVMEnvVar[]; error: string } {
  const env: LightVMEnvVar[] = [];
  const seen = new Set<string>();
  const lines = text.split("\n");
  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index].replace(/\r$/, "");
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("#")) {
      continue;
    }
    const assignment = line.trimStart().startsWith("export ")
      ? line.trimStart().slice("export ".length)
      : line;
    const separator = assignment.indexOf("=");
    if (separator < 1) {
      return {
        env: [],
        error: `Environment line ${index + 1} must be NAME=value.`,
      };
    }
    const name = assignment.slice(0, separator).trim();
    const value = assignment.slice(separator + 1);
    if (!ENV_NAME_PATTERN.test(name)) {
      return {
        env: [],
        error: `Environment variable ${name || "(empty)"} on line ${index + 1} must match [A-Za-z_][A-Za-z0-9_]*.`,
      };
    }
    if (name.length > 128) {
      return {
        env: [],
        error: `Environment variable ${name} on line ${index + 1} is too long.`,
      };
    }
    if (value.length > 4096) {
      return {
        env: [],
        error: `Environment value for ${name} on line ${index + 1} is too long.`,
      };
    }
    if (value.includes("\x00")) {
      return {
        env: [],
        error: `Environment value for ${name} on line ${index + 1} contains a NUL character.`,
      };
    }
    if (seen.has(name)) {
      return {
        env: [],
        error: `Environment variable ${name} is duplicated (line ${index + 1}).`,
      };
    }
    seen.add(name);
    env.push({ name, value });
  }
  return { env, error: "" };
}

function vmInterfaces(vm: LightVM | null): VMInterface[] {
  return Array.isArray(vm?.interfaces) ? vm.interfaces : [];
}

export function LightVMDialog({
  vm,
  kernels,
  initramfs,
  interfaces,
  onClose,
  onSaved,
}: LightVMDialogProps) {
  const editing = vm !== null;
  const [name, setName] = useState(vm?.name || "");
  const [description, setDescription] = useState(vm?.description || "");
  const [vcpus, setVCPUs] = useState(String(vm?.vcpus ?? 1));
  const [memoryMB, setMemoryMB] = useState(String(vm?.memory_mb ?? 512));
  const [consoleEnabled, setConsoleEnabled] = useState(Boolean(vm?.console_enabled));
  const [cgroupSettings, setCgroupSettings] = useState<CgroupSettings | null>(null);
  const [cgroupClass, setCgroupClass] = useState(vm?.cgroup_class || "");
  const [kernelID, setKernelID] = useState(vm?.kernel_id ? String(vm.kernel_id) : "");
  const [initramfsID, setInitramfsID] = useState(
    vm?.initramfs_id ? String(vm.initramfs_id) : ""
  );
  const [image, setImage] = useState(vm?.image || "");
  const [environment, setEnvironment] = useState(() => envText(vm));
  const [consoles, setConsoles] = useState<ConsoleRow[]>(() => consoleRows(vm));
  const [executablePath, setExecutablePath] = useState(vm?.executable_path || "");
  const [argsText, setArgsText] = useState(
    Array.isArray(vm?.executable_args) ? vm.executable_args.join("\n") : ""
  );
  const [append, setAppend] = useState(vm ? vm.append || "" : DEFAULT_APPEND);
  const [selectedInterfaces, setSelectedInterfaces] = useState<VMInterface[]>(() =>
    vmInterfaces(vm)
  );
  const [interfaceChoice, setInterfaceChoice] = useState("");
  const [interfaceHint, setInterfaceHint] = useState("");
  const [labels, setLabels] = useEntityLabelDraft("lightvm", vm?.id);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const dialogRef = useRef<HTMLDialogElement | null>(null);
  const nameRef = useRef<HTMLInputElement | null>(null);
  const formRef = useRef<HTMLFormElement | null>(null);

  const selectedInterfaceIDs = new Set(selectedInterfaces.map((iface) => iface.id));
  const cgroupClasses = Array.isArray(cgroupSettings?.classes) ? cgroupSettings.classes : [];
  const leafCgroupClasses = cgroupClasses.filter(
    (item) => !cgroupClasses.some((other) => other.name.startsWith(item.name + "/"))
  );

  const availableInterfaces = interfaces.filter((iface) => {
    if (selectedInterfaceIDs.has(iface.id)) {
      return false;
    }
    if (iface.attached_vm_id) {
      return false;
    }
    return !iface.attached_lightvm_id || (vm !== null && iface.attached_lightvm_id === vm.id);
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
    if (availableInterfaces.length === 0 && selectedInterfaces.length === 0) {
      setInterfaceHint("Create a Rookery Virtual Interface on the Network tab first.");
    } else {
      setInterfaceHint(
        "Choose an interface and click Add. Interfaces attached to VMs or other LightVMs are hidden."
      );
    }
  }, [availableInterfaces.length, selectedInterfaces.length]);

  const close = () => {
    if (!saving) {
      dialogRef.current?.close();
    }
  };

  const addInterface = () => {
    const id = Number(interfaceChoice);
    if (!id) {
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
    setSelectedInterfaces((current) => current.filter((iface) => iface.id !== id));
  };

  const updateConsole = (index: number, name: string) => {
    setConsoles((current) =>
      current.map((console, position) => (position === index ? { ...console, name } : console))
    );
  };

  const removeConsole = (index: number) => {
    setConsoles((current) => current.filter((_, position) => position !== index));
  };

  const addConsole = () => {
    setConsoles((current) => [
      ...current,
      { id: 0, name: nextConsoleName(current) },
    ]);
  };

  const submit = async (event: Event) => {
    event.preventDefault();
    if (saving || !formRef.current?.reportValidity()) {
      return;
    }
    const selectedKernel = Number(kernelID);
    const selectedInitramfs = Number(initramfsID);
    if (!selectedKernel || !selectedInitramfs) {
      setError("Select a kernel and an initramfs image.");
      return;
    }
    if (consoles.length === 0) {
      setError("Add at least one console.");
      return;
    }
    const normalizedConsoles: ConsoleRow[] = [];
    const seenConsoles = new Set<string>();
    for (const console of consoles) {
      const consoleName = console.name.trim();
      if (!consoleName) {
        setError("Every console needs a name.");
        return;
      }
      const key = consoleName.toLowerCase();
      if (seenConsoles.has(key)) {
        setError(`Console ${consoleName} is duplicated.`);
        return;
      }
      seenConsoles.add(key);
      normalizedConsoles.push({ id: console.id, name: consoleName });
    }
    const parsedEnv = parseEnvText(environment);
    if (parsedEnv.error) {
      setError(parsedEnv.error);
      return;
    }
    const executableArgs = argsText
      .split("\n")
      .map((line) => line.replace(/\r$/, ""))
      .filter((line) => line.length > 0);

    const payload = {
      name: name.trim(),
      description: description.trim(),
      vcpus: Number(vcpus),
      memory_mb: Number(memoryMB),
      console_enabled: consoleEnabled,
      cgroup_class: cgroupClass,
      kernel_id: selectedKernel,
      initramfs_id: selectedInitramfs,
      image: image.trim(),
      env: parsedEnv.env,
      executable_path: executablePath.trim(),
      executable_args: executableArgs,
      append: append.trim(),
      consoles: normalizedConsoles,
      interface_ids: selectedInterfaces.map((iface) => iface.id),
    };

    setError("");
    setSaving(true);
    try {
      const saved = await api<LightVM>(
        editing ? `/api/lightvm/instances/${vm.id}` : "/api/lightvm/instances",
        {
          method: editing ? "PUT" : "POST",
          body: JSON.stringify(payload),
        }
      );
      await saveEntityLabelsWithToast("lightvm", saved.id, saved.name || name.trim(), labels);
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
      id="lightvm-dialog"
      aria-labelledby="lightvm-dialog-title"
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
            <p class="eyebrow">{editing ? "Edit LightVM" : "New LightVM"}</p>
            <h2 id="lightvm-dialog-title">
              {editing ? `Edit ${vm?.name}` : "Create LightVM"}
            </h2>
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
              placeholder="app-01"
              value={name}
              onInput={(event) => setName(event.currentTarget.value)}
            />
            <span class="field-hint">Letters, digits, dots, dashes and underscores.</span>
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

          <label class="field">
            <span class="field-label">Kernel</span>
            <select
              required
              value={kernelID}
              onChange={(event) => setKernelID(event.currentTarget.value)}
            >
              <option value="">Select a kernel…</option>
              {kernels.map((item) => (
                <option value={String(item.id)} key={item.id}>
                  {item.name}
                </option>
              ))}
            </select>
            <span class="field-hint">
              {kernels.length === 0 ? "Upload a kernel on the Kernels subtab first." : ""}
            </span>
          </label>

          <label class="field">
            <span class="field-label">Initramfs</span>
            <select
              required
              value={initramfsID}
              onChange={(event) => setInitramfsID(event.currentTarget.value)}
            >
              <option value="">Select an initramfs…</option>
              {initramfs.map((item) => (
                <option value={String(item.id)} key={item.id}>
                  {item.name}
                </option>
              ))}
            </select>
            <span class="field-hint">
              {initramfs.length === 0 ? "Upload an initramfs on the Initramfs subtab first." : ""}
            </span>
          </label>

          <label class="field field-wide">
            <span class="field-label">Kernel append</span>
            <input
              type="text"
              maxlength={4096}
              autocomplete="off"
              spellcheck={false}
              placeholder={DEFAULT_APPEND}
              value={append}
              onInput={(event) => setAppend(event.currentTarget.value)}
            />
            <span class="field-hint">
              Kernel command line appended at boot. Defaults to {DEFAULT_APPEND}.
            </span>
          </label>

          <label class="field field-wide">
            <span class="field-label">Container image</span>
            <input
              type="text"
              required
              maxlength={512}
              autocomplete="off"
              spellcheck={false}
              placeholder="ghcr.io/example/app:latest"
              value={image}
              onInput={(event) => setImage(event.currentTarget.value)}
            />
            <span class="field-hint">Docker/OCI image reference used by the LightVM runtime.</span>
          </label>

          <label class="field field-wide">
            <span class="field-label">Executable path</span>
            <input
              type="text"
              required
              maxlength={4096}
              autocomplete="off"
              spellcheck={false}
              placeholder="/usr/local/bin/app"
              value={executablePath}
              onInput={(event) => setExecutablePath(event.currentTarget.value)}
            />
            <span class="field-hint">Absolute path to the executable inside the container image.</span>
          </label>

          <label class="field field-wide">
            <span class="field-label">Arguments</span>
            <textarea
              rows={4}
              spellcheck={false}
              placeholder={"--listen\n:8080"}
              value={argsText}
              onInput={(event) => setArgsText(event.currentTarget.value)}
            />
            <span class="field-hint">
              One argument per line. Rookery writes the executable path and these
              arguments to an executable <code>.entrypoint</code> script at the image root.
            </span>
          </label>

          <label class="field field-wide">
            <span class="field-label">Environment variables</span>
            <textarea
              rows={6}
              spellcheck={false}
              autocomplete="off"
              placeholder={"APP_ENV=production\nDATABASE_URL=postgres://db/app\nLOG_LEVEL=info"}
              value={environment}
              onInput={(event) => setEnvironment(event.currentTarget.value)}
            />
            <span class="field-hint">
              One <code>NAME=value</code> per line. Rookery writes these to{" "}
              <code>.env</code> at the root of the mounted image before starting the
              LightVM. Blank lines, <code>export </code> prefixes, and lines beginning
              with <code>#</code> are ignored.
            </span>
          </label>

          <div class="field field-wide">
            <span class="field-label">Consoles</span>
            <div class="console-editor">
              {consoles.map((console, index) => (
                <div class="console-row" key={`${console.id}-${index}`}>
                  <input
                    type="text"
                    required
                    maxlength={64}
                    autocomplete="off"
                    spellcheck={false}
                    placeholder={index === 0 ? "console" : `console-${index + 1}`}
                    aria-label={`Console name ${index + 1}`}
                    value={console.name}
                    onInput={(event) => updateConsole(index, event.currentTarget.value)}
                  />
                  <button
                    class="row-action danger"
                    type="button"
                    disabled={consoles.length <= 1}
                    title={consoles.length <= 1 ? "A LightVM must have at least one console." : ""}
                    onClick={() => removeConsole(index)}
                  >
                    Remove
                  </button>
                </div>
              ))}
              <div>
                <button
                  class="button ghost"
                  type="button"
                  disabled={consoles.length >= 32}
                  onClick={addConsole}
                >
                  Add console
                </button>
              </div>
            </div>
            <span class="field-hint">
              The first console is exposed as <code>org.example.console</code>; additional ports
              use <code>org.example.console.N</code>. Open the terminal page to choose which
              console to use.
            </span>
          </div>

          <label class="field field-wide checkbox-field">
            <span class="field-label">Console access</span>
            <span class="checkbox-row">
              <input
                type="checkbox"
                checked={consoleEnabled}
                onChange={(event) => setConsoleEnabled(event.currentTarget.checked)}
              />
              <span>Allow end users to open this LightVM's consoles</span>
            </span>
            <span class="field-hint">
              Applies to every console port; disabling it closes existing console sessions.
            </span>
          </label>

          <div class="field field-wide">
            <label class="field">
              <span class="field-label">
                Cgroup class <em>optional</em>
              </span>
              <select value={cgroupClass} onChange={(event) => setCgroupClass(event.currentTarget.value)}>
                <option value="">Shared cgroup (host defaults)</option>
                {!leafCgroupClasses.some((item) => item.name === cgroupClass) && cgroupClass ? (
                  <option value={cgroupClass}>{cgroupClass} (not a leaf class)</option>
                ) : null}
                {leafCgroupClasses.map((item) => (
                  <option value={item.name} key={item.name}>
                    {item.name}
                  </option>
                ))}
              </select>
              <span class="field-hint">
                Select a leaf class defined in Settings.
                {cgroupSettings && !cgroupSettings.enabled
                  ? " Host-wide cgroup limits are disabled in Settings; the class applies when they are enabled."
                  : ""}
              </span>
            </label>
          </div>

          <div class="field field-wide">
            <span class="field-label">Network interfaces <em>optional</em></span>
            <div class="interface-picker">
              <select
                value={interfaceChoice}
                disabled={availableInterfaces.length === 0}
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
                disabled={availableInterfaces.length === 0}
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
              placeholder="What is this LightVM for?"
              value={description}
              onInput={(event) => setDescription(event.currentTarget.value)}
            />
          </label>

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
          <button class="button primary" type="submit" disabled={saving}>
            {saving
              ? editing
                ? "Saving…"
                : "Creating…"
              : editing
                ? "Save changes"
                : "Create LightVM"}
          </button>
        </footer>
      </form>
    </dialog>
  );
}
