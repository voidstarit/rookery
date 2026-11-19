import { useEffect, useRef, useState } from "preact/hooks";
import { api } from "../../shared/api";
import { formatBytesIEC } from "../../shared/format";
import { LabelsInput, saveEntityLabelsWithToast, useEntityLabelDraft } from "../../shared/labels";
import type {
  CgroupSettings,
  Disk,
  LightVM,
  LightVMConfigFile,
  LightVMEnvVar,
  LightVMInitramfs,
  LightVMKernel,
  LightVMVirtioFSShare,
  VMDisk,
  VMInterface,
  VirtualInterface,
} from "../types";

type LightVMDialogProps = {
  vm: LightVM | null;
  kernels: LightVMKernel[];
  initramfs: LightVMInitramfs[];
  interfaces: VirtualInterface[];
  disks: Disk[];
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

const CONFIG_FILE_MAX = 64;
const CONFIG_FILE_MAX_BYTES = 1 << 20;
const CONFIG_FILES_MAX_BYTES = 4 << 20;

function contentBytes(value: string): number {
  return new TextEncoder().encode(value).length;
}

function configFileNameError(name: string, existing: LightVMConfigFile[]): string {
  const trimmed = name.trim();
  if (!trimmed) {
    return "Config file name is required.";
  }
  if (trimmed.length > 255) {
    return "Config file name must be at most 255 characters.";
  }
  if (trimmed.startsWith("/")) {
    return "Config file name must be relative.";
  }
  if (trimmed.endsWith("/")) {
    return "Config file name must not end with a slash.";
  }
  if (/[\r\n\x00]/.test(trimmed)) {
    return "Config file name must be a single line without NUL characters.";
  }
  const parts = trimmed.split("/");
  if (parts.some((part) => part === "" || part === "." || part === "..")) {
    return "Config file name has an invalid path component.";
  }
  if (parts[0] === "env" || parts[0] === "startup-cmd") {
    return `Config file name "${trimmed}" is reserved for Rookery.`;
  }
  if (existing.some((file) => file.name === trimmed)) {
    return `Config file "${trimmed}" is already added.`;
  }
  if (existing.some((file) => file.name.startsWith(trimmed + "/") || trimmed.startsWith(file.name + "/"))) {
    return `Config file "${trimmed}" conflicts with another file path.`;
  }
  return "";
}

function vmInterfaces(vm: LightVM | null): VMInterface[] {
  return Array.isArray(vm?.interfaces) ? vm.interfaces : [];
}

function diskSerialPayload(disks: VMDisk[]): Record<string, string> {
  const serials: Record<string, string> = {};
  for (const disk of disks) {
    const serial = (disk.serial ?? "").trim();
    if (serial) {
      serials[String(disk.id)] = serial;
    }
  }
  return serials;
}

export function LightVMDialog({
  vm,
  kernels,
  initramfs,
  interfaces,
  disks,
  onClose,
  onSaved,
}: LightVMDialogProps) {
  const editing = vm !== null;
  const [name, setName] = useState(vm?.name || "");
  const [description, setDescription] = useState(vm?.description || "");
  const [vcpus, setVCPUs] = useState(String(vm?.vcpus ?? 1));
  const [memoryMB, setMemoryMB] = useState(String(vm?.memory_mb ?? 512));
  const [consoleEnabled, setConsoleEnabled] = useState(Boolean(vm?.console_enabled));
  const [ephemeral, setEphemeral] = useState(Boolean(vm?.ephemeral));
  const [cgroupSettings, setCgroupSettings] = useState<CgroupSettings | null>(null);
  const [cgroupClass, setCgroupClass] = useState(vm?.cgroup_class || "");
  const [kernelID, setKernelID] = useState(vm?.kernel_id ? String(vm.kernel_id) : "");
  const [initramfsID, setInitramfsID] = useState(
    vm?.initramfs_id ? String(vm.initramfs_id) : ""
  );
  const [image, setImage] = useState(vm?.image || "");
  const [environment, setEnvironment] = useState(() => envText(vm));
  const [configFiles, setConfigFiles] = useState<LightVMConfigFile[]>(() =>
    Array.isArray(vm?.config_files) ? vm.config_files : []
  );
  const [configFileName, setConfigFileName] = useState("");
  const [configFileContent, setConfigFileContent] = useState("");
  const [consoles, setConsoles] = useState<ConsoleRow[]>(() => consoleRows(vm));
  const [executionMode, setExecutionMode] = useState<"path" | "script">(
    vm?.executable_script ? "script" : "path"
  );
  const [executablePath, setExecutablePath] = useState(vm?.executable_path || "");
  const [executableScript, setExecutableScript] = useState(vm?.executable_script || "");
  const [argsText, setArgsText] = useState(
    Array.isArray(vm?.executable_args) ? vm.executable_args.join("\n") : ""
  );
  const [append, setAppend] = useState(vm ? vm.append || "" : DEFAULT_APPEND);
  const [selectedInterfaces, setSelectedInterfaces] = useState<VMInterface[]>(() =>
    vmInterfaces(vm)
  );
  const [selectedDisks, setSelectedDisks] = useState<VMDisk[]>(() => {
    if (Array.isArray(vm?.disks) && vm.disks.length > 0) {
      return vm.disks;
    }
    const ids = new Set(vm?.disk_ids ?? []);
    return disks.filter((disk) => ids.has(disk.id));
  });
  const [diskChoice, setDiskChoice] = useState("");
  const [shares, setShares] = useState<LightVMVirtioFSShare[]>(() =>
    Array.isArray(vm?.virtiofs_shares) ? vm.virtiofs_shares : []
  );
  const [sharePath, setSharePath] = useState("");
  const [shareTag, setShareTag] = useState("");
  const [shareReadOnly, setShareReadOnly] = useState(true);
  const [interfaceChoice, setInterfaceChoice] = useState("");
  const [interfaceHint, setInterfaceHint] = useState("");
  const [labels, setLabels] = useEntityLabelDraft("lightvm", vm?.id);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const dialogRef = useRef<HTMLDialogElement | null>(null);
  const nameRef = useRef<HTMLInputElement | null>(null);
  const formRef = useRef<HTMLFormElement | null>(null);

  const selectedInterfaceIDs = new Set(selectedInterfaces.map((iface) => iface.id));
  const selectedDiskIDs = new Set(selectedDisks.map((disk) => disk.id));
  const availableDisks = disks.filter((disk) => {
    if (selectedDiskIDs.has(disk.id) || disk.is_template || disk.deletion_state) {
      return false;
    }
    if (disk.read_only) {
      // Read-only disks are shareable across VMs and LightVMs.
      return true;
    }
    if (disk.attached_vm_id) {
      return false;
    }
    return !disk.attached_lightvm_id || (vm !== null && disk.attached_lightvm_id === vm.id);
  });
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

  const addDisk = () => {
    const id = Number(diskChoice);
    if (!id) {
      return;
    }
    const disk =
      disks.find((item) => item.id === id) ||
      selectedDisks.find((item) => item.id === id);
    if (disk) {
      setSelectedDisks((current) =>
        current.some((item) => item.id === disk.id) ? current : [...current, disk]
      );
      setDiskChoice("");
    }
  };

  const removeDisk = (id: number) => {
    setSelectedDisks((current) => current.filter((disk) => disk.id !== id));
  };

  const setDiskSerial = (id: number, serial: string) => {
    setSelectedDisks((current) =>
      current.map((disk) => (disk.id === id ? { ...disk, serial } : disk))
    );
  };

  const addShare = () => {
    const path = sharePath.trim();
    const tag = shareTag.trim();
    if (!path.startsWith("/")) {
      setError("Host directory path must be absolute.");
      return;
    }
    if (!/^[A-Za-z0-9][A-Za-z0-9._-]*$/.test(tag) || tag.length > 64) {
      setError("Share tag must start with a letter or digit and contain only letters, digits, dots, dashes, and underscores.");
      return;
    }
    if (tag.toLowerCase() === "rootfs") {
      setError('The tag "rootfs" is reserved for the container root filesystem.');
      return;
    }
    if (shares.some((share) => share.tag.toLowerCase() === tag.toLowerCase())) {
      setError(`Share tag "${tag}" is already used.`);
      return;
    }
    setShares((current) => [
      ...current,
      { host_path: path, tag, read_only: shareReadOnly },
    ]);
    setSharePath("");
    setShareTag("");
    setShareReadOnly(true);
    setError("");
  };

  const removeShare = (tag: string) => {
    setShares((current) => current.filter((share) => share.tag !== tag));
  };

  const configFilesTotalBytes = (files: LightVMConfigFile[]) =>
    files.reduce((total, file) => total + contentBytes(file.content), 0);

  const removeConfigFile = (name: string) => {
    setConfigFiles((current) => current.filter((file) => file.name !== name));
  };

  const addConfigFile = () => {
    const name = configFileName.trim();
    if (configFiles.length >= CONFIG_FILE_MAX) {
      setError(`At most ${CONFIG_FILE_MAX} config files are allowed.`);
      return;
    }
    const nameError = configFileNameError(name, configFiles);
    if (nameError) {
      setError(nameError);
      return;
    }
    const size = contentBytes(configFileContent);
    if (size > CONFIG_FILE_MAX_BYTES) {
      setError(`Config file "${name}" must be at most ${formatBytesIEC(CONFIG_FILE_MAX_BYTES)}.`);
      return;
    }
    if (configFilesTotalBytes(configFiles) + size > CONFIG_FILES_MAX_BYTES) {
      setError(`Config files must total at most ${formatBytesIEC(CONFIG_FILES_MAX_BYTES)}.`);
      return;
    }
    setConfigFiles((current) => [...current, { name, content: configFileContent }]);
    setConfigFileName("");
    setConfigFileContent("");
    setError("");
  };

  const uploadConfigFiles = async (event: Event) => {
    const input = event.currentTarget as HTMLInputElement;
    const selected = Array.from(input.files ?? []);
    input.value = "";
    if (selected.length === 0) {
      return;
    }
    const next = [...configFiles];
    let total = configFilesTotalBytes(next);
    let failure = "";
    for (const file of selected) {
      if (next.length >= CONFIG_FILE_MAX) {
        failure = `At most ${CONFIG_FILE_MAX} config files are allowed.`;
        break;
      }
      if (file.size > CONFIG_FILE_MAX_BYTES) {
        failure = `Config file "${file.name}" must be at most ${formatBytesIEC(CONFIG_FILE_MAX_BYTES)}.`;
        continue;
      }
      const nameError = configFileNameError(file.name, next);
      if (nameError) {
        failure = nameError;
        continue;
      }
      const content = await file.text();
      const size = contentBytes(content);
      if (size > CONFIG_FILE_MAX_BYTES) {
        failure = `Config file "${file.name}" must be at most ${formatBytesIEC(CONFIG_FILE_MAX_BYTES)}.`;
        continue;
      }
      if (total + size > CONFIG_FILES_MAX_BYTES) {
        failure = `Config files must total at most ${formatBytesIEC(CONFIG_FILES_MAX_BYTES)}.`;
        break;
      }
      next.push({ name: file.name.trim(), content });
      total += size;
    }
    setConfigFiles(next);
    if (failure) {
      setError(failure);
    } else {
      setError("");
    }
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

  const diskBacked = image.trim() === "";

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
    if (diskBacked && selectedDisks.length === 0) {
      setError("Select at least one disk or provide a container image.");
      return;
    }
    const serials = selectedDisks
      .map((disk) => (disk.serial ?? "").trim())
      .filter((serial) => serial.length > 0);
    if (new Set(serials).size !== serials.length) {
      setError("Disk serials must be unique within the LightVM.");
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
    const startupPath = executionMode === "path" ? executablePath.trim() : "";
    const startupScript = executionMode === "script" ? executableScript.trim() : "";
    if (!diskBacked && !startupPath && !startupScript) {
      setError("Provide an executable path or an inline script for a container-image LightVM.");
      return;
    }
    if (executionMode === "path" && !startupPath && executableArgs.length > 0) {
      setError("Add an executable path before adding arguments.");
      return;
    }

    const payload = {
      name: name.trim(),
      description: description.trim(),
      vcpus: Number(vcpus),
      memory_mb: Number(memoryMB),
      console_enabled: consoleEnabled,
      ephemeral,
      cgroup_class: cgroupClass,
      kernel_id: selectedKernel,
      initramfs_id: selectedInitramfs,
      image: image.trim(),
      env: parsedEnv.env,
      config_files: diskBacked ? configFiles : [],
      executable_path: executionMode === "path" ? executablePath.trim() : "",
      executable_script: executionMode === "script" ? executableScript : "",
      executable_args: executionMode === "path" ? executableArgs : [],
      append: append.trim(),
      consoles: normalizedConsoles,
      interface_ids: selectedInterfaces.map((iface) => iface.id),
      disk_ids: selectedDisks.map((disk) => disk.id),
      disk_serials: diskSerialPayload(selectedDisks),
      // replace_disk_ids only exists on UpdateLightVMRequest. Sending it
      // (even as false) to CreateLightVMRequest is rejected by protojson.
      ...(editing ? { replace_disk_ids: true } : {}),
      virtiofs_shares: shares.map((share) => ({
        host_path: share.host_path,
        tag: share.tag,
        read_only: share.read_only,
      })),
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
              maxlength={512}
              autocomplete="off"
              spellcheck={false}
              placeholder="ghcr.io/example/app:latest"
              value={image}
              onInput={(event) => setImage(event.currentTarget.value)}
            />
            <span class="field-hint">
              Optional Docker/OCI image reference. Leave empty to boot from the
              selected disks with the kernel and initramfs.
            </span>
          </label>

          <div class="field field-wide" role="radiogroup" aria-label="Startup command">
            <span class="field-label">Startup command</span>
            <label class="radio-row">
              <input
                type="radio"
                name="execution-mode"
                checked={executionMode === "path"}
                onChange={() => setExecutionMode("path")}
              />
              <span>Executable path</span>
            </label>
            <label class="radio-row">
              <input
                type="radio"
                name="execution-mode"
                checked={executionMode === "script"}
                onChange={() => setExecutionMode("script")}
              />
              <span>Inline executable script</span>
            </label>
          </div>

          {executionMode === "path" ? (
            <>
              <label class="field field-wide">
                <span class="field-label">Executable path</span>
                <input
                  type="text"
                  required={!diskBacked}
                  maxlength={4096}
                  autocomplete="off"
                  spellcheck={false}
                  placeholder="/usr/local/bin/app"
                  value={executablePath}
                  onInput={(event) => setExecutablePath(event.currentTarget.value)}
                />
                <span class="field-hint">
                  {diskBacked
                    ? "Absolute path inside the guest root filesystem. Optional; the bundled disk-root/overlay-root initramfs runs the published startup-cmd as PID 1."
                    : "Absolute path to the executable inside the container image."}
                </span>
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
                  {diskBacked
                    ? "One argument per line. Written with the executable path to the startup-cmd file in the rookery-config device."
                    : "One argument per line. Rookery writes the executable path and these arguments to an executable .entrypoint script at the image root."}
                </span>
              </label>
            </>
          ) : (
            <label class="field field-wide">
              <span class="field-label">Executable script</span>
              <textarea
                rows={12}
                required={!diskBacked}
                maxlength={1048576}
                autocomplete="off"
                spellcheck={false}
                placeholder={"#!/bin/sh\necho \"hello from LightVM\"\nexec /usr/local/bin/app"}
                value={executableScript}
                onInput={(event) => setExecutableScript(event.currentTarget.value)}
              />
              <span class="field-hint">
                {diskBacked
                  ? "Written to the startup-cmd file in the rookery-config device. The bundled disk-root/overlay-root initramfs extracts it to /run/rookery and runs it as PID 1 unless an explicit init= is set."
                  : "Copied verbatim to /.entrypoint in the image root and made executable. A #!/bin/sh header is added when the script does not start with a shebang. Executable arguments are not used in script mode."}
              </span>
            </label>
          )}

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
              {diskBacked
                ? "One NAME=value per line. Rookery creates a read-only virtio-blk device with serial rookery-config containing an uncompressed tar with env and startup-cmd. The bundled disk-root/overlay-root initramfs extracts it to /run/rookery, loads env, and runs startup-cmd. Blank lines, export prefixes, and # comments are ignored."
                : "One NAME=value per line. Rookery writes these to .env at the root of the mounted image before starting the LightVM. Blank lines, export prefixes, and lines beginning with # are ignored."}
            </span>
          </label>

          {diskBacked ? (
            <div class="field field-wide">
              <span class="field-label">
                Config files <em>optional</em>
              </span>
              <label class="field field-wide">
                <span class="field-label">Upload files</span>
                <input
                  type="file"
                  multiple
                  disabled={saving}
                  onChange={uploadConfigFiles}
                />
                <span class="field-hint">
                  Text files are added to the rookery-config tar and extracted
                  under /run/rookery by the bundled disk-root/overlay-root
                  initramfs. The names env and startup-cmd are reserved.
                </span>
              </label>
              <div class="form-grid">
                <label class="field">
                  <span class="field-label">File name</span>
                  <input
                    type="text"
                    maxlength={255}
                    autocomplete="off"
                    spellcheck={false}
                    placeholder="app/config.yaml"
                    value={configFileName}
                    onInput={(event) => setConfigFileName(event.currentTarget.value)}
                  />
                </label>
                <label class="field field-wide">
                  <span class="field-label">File content</span>
                  <textarea
                    rows={6}
                    autocomplete="off"
                    spellcheck={false}
                    placeholder="key: value"
                    value={configFileContent}
                    onInput={(event) => setConfigFileContent(event.currentTarget.value)}
                  />
                </label>
                <div class="field field-wide">
                  <button
                    class="button ghost"
                    type="button"
                    disabled={saving || configFiles.length >= CONFIG_FILE_MAX}
                    onClick={addConfigFile}
                  >
                    Add config file
                  </button>
                </div>
              </div>
              <ul class="interface-list">
                {configFiles.length === 0 ? (
                  <li class="empty">No config files added</li>
                ) : (
                  configFiles.map((file) => (
                    <li key={file.name}>
                      <span class="interface-list-name">
                        {file.name}
                        <span class="interface-list-network">
                          {formatBytesIEC(contentBytes(file.content))}
                        </span>
                      </span>
                      <button
                        class="row-action danger"
                        type="button"
                        onClick={() => removeConfigFile(file.name)}
                      >
                        Remove
                      </button>
                    </li>
                  ))
                )}
              </ul>
            </div>
          ) : null}

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

          <label class="field field-wide checkbox-field">
            <span class="field-label">Ephemeral LightVM</span>
            <span class="checkbox-row">
              <input
                type="checkbox"
                checked={ephemeral}
                onChange={(event) => setEphemeral(event.currentTarget.checked)}
              />
              <span>Terminate this LightVM when its guest exits</span>
            </span>
            <span class="field-hint">
              When the guest kernel reboots, panics, or powers off, Rookery stops QEMU
              and removes the virtiofsd process, attempt container, console sockets,
              and config drive, then deletes this LightVM definition. Its disk and
              network-interface attachments are released so other VMs can use them;
              the disk and network records themselves are kept.
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
            <span class="field-label">Disks <em>optional</em></span>
            <div class="interface-picker">
              <select
                value={diskChoice}
                disabled={availableDisks.length === 0}
                onChange={(event) => setDiskChoice(event.currentTarget.value)}
              >
                <option value="">
                  {availableDisks.length > 0 ? "Select a disk…" : "No disks available"}
                </option>
                {availableDisks.map((disk) => (
                  <option value={String(disk.id)} key={disk.id}>
                    {disk.name} · {disk.size_gb} GB{disk.read_only ? " · read-only" : ""}
                  </option>
                ))}
              </select>
              <button
                class="button ghost"
                type="button"
                disabled={availableDisks.length === 0}
                onClick={addDisk}
              >
                Add
              </button>
            </div>
            <ul class="interface-list">
              {selectedDisks.length === 0 ? (
                <li class="empty">No disks selected</li>
              ) : (
                selectedDisks.map((disk) => (
                  <li key={disk.id}>
                    <span class="interface-list-name">
                      {disk.name}
                      <span class="interface-list-network">
                        {disk.size_gb} GB · {disk.source || "lvm-thin"}
                        {disk.read_only ? " · read-only" : ""}
                      </span>
                    </span>
                    <label class="disk-serial">
                      <span class="sr-only">Serial for {disk.name}</span>
                      <input
                        type="text"
                        maxlength={20}
                        pattern="[A-Za-z0-9._:-]*"
                        autocomplete="off"
                        spellcheck={false}
                        placeholder="serial (optional)"
                        title="Guest-visible virtio-blk serial, up to 20 bytes"
                        value={disk.serial ?? ""}
                        onInput={(event) => setDiskSerial(disk.id, event.currentTarget.value)}
                      />
                    </label>
                    <button
                      class="row-action danger"
                      type="button"
                      onClick={() => removeDisk(disk.id)}
                    >
                      Remove
                    </button>
                  </li>
                ))
              )}
            </ul>
            <span class="field-hint">
              Disks are attached in this order. With a container image, root remains the
              image and the guest initramfs mounts additional disks. With no image, the
              guest boots from these disks; set the kernel command line as needed. Set an
              optional serial so the guest can identify each disk. A disk-backed LightVM
              with environment variables or a startup command also receives a read-only
              rookery-config device.
            </span>
          </div>

          <div class="field field-wide">
            <span class="field-label">Host directory shares <em>optional</em></span>
            <div class="form-grid">
              <label class="field field-wide">
                <span class="field-label">Host path</span>
                <input
                  type="text"
                  autocomplete="off"
                  spellcheck={false}
                  placeholder="/home/user/light-vm-1"
                  value={sharePath}
                  onInput={(event) => setSharePath(event.currentTarget.value)}
                />
                <span class="field-hint">Absolute path to an existing directory on the host.</span>
              </label>
              <label class="field">
                <span class="field-label">Guest tag</span>
                <input
                  type="text"
                  maxlength={64}
                  autocomplete="off"
                  spellcheck={false}
                  placeholder="tag1"
                  value={shareTag}
                  onInput={(event) => setShareTag(event.currentTarget.value)}
                />
              </label>
              <label class="field checkbox-field">
                <span class="field-label">Access</span>
                <span class="checkbox-row">
                  <input
                    type="checkbox"
                    checked={shareReadOnly}
                    onChange={(event) => setShareReadOnly(event.currentTarget.checked)}
                  />
                  <span>Read-only</span>
                </span>
              </label>
              <div class="field field-wide">
                <button class="button ghost" type="button" onClick={addShare}>
                  Add share
                </button>
              </div>
            </div>
            <ul class="interface-list">
              {shares.length === 0 ? (
                <li class="empty">No host directories shared</li>
              ) : (
                shares.map((share) => (
                  <li key={share.tag}>
                    <span class="interface-list-name">
                      {share.tag}
                      <span class="interface-list-network">
                        {share.host_path} · {share.read_only ? "read-only" : "read-write"}
                      </span>
                    </span>
                    <button
                      class="row-action danger"
                      type="button"
                      onClick={() => removeShare(share.tag)}
                    >
                      Remove
                    </button>
                  </li>
                ))
              )}
            </ul>
            <span class="field-hint">
              Each share gets a separate virtiofs device. The guest filesystem decides
              whether and where to mount each tag. The tag <code>rootfs</code> is reserved.
            </span>
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
