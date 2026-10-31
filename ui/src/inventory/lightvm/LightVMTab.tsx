import { useEffect, useState } from "preact/hooks";
import { api } from "../../shared/api";
import { EntityLabels, useEntityLabels } from "../../shared/labels";
import { showToast } from "../../shared/toast";
import type {
  LightVM,
  LightVMInitramfs,
  LightVMKernel,
  StatusResponse,
  VirtualInterface,
} from "../types";
import { navigate, useRoute } from "../router";
import {
  ArtifactDialog,
  type LightVMArtifact,
  type LightVMArtifactKind,
} from "./ArtifactDialog";
import { LightVMDialog } from "./LightVMDialog";

const SECTIONS = [
  { id: "kernels", label: "Kernels" },
  { id: "initramfs", label: "Initramfs" },
  { id: "instances", label: "LightVMs" },
] as const;

type Section = (typeof SECTIONS)[number]["id"];

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

function formatDate(value: string | undefined): string {
  if (!value) {
    return "—";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "—";
  }
  return date.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}

function sortArtifacts<T extends { name: string }>(items: T[]): T[] {
  return [...items].sort((a, b) =>
    a.name.localeCompare(b.name, undefined, { sensitivity: "base" })
  );
}

function sectionFor(kind: LightVMArtifactKind): Section {
  return kind === "kernel" ? "kernels" : "initramfs";
}

function endpointFor(kind: LightVMArtifactKind): string {
  return kind === "kernel" ? "/api/lightvm/kernels" : "/api/lightvm/initramfs";
}

export function LightVMTab() {
  const [kernels, setKernels] = useState<LightVMKernel[]>([]);
  const [initramfs, setInitramfs] = useState<LightVMInitramfs[]>([]);
  const [lightvms, setLightVMs] = useState<LightVM[]>([]);
  const [interfaces, setInterfaces] = useState<VirtualInterface[]>([]);
  const [section, setSection] = useState<Section>("kernels");
  const [loading, setLoading] = useState(true);
  const [kernelError, setKernelError] = useState("");
  const [initramfsError, setInitramfsError] = useState("");
  const [lightVMError, setLightVMError] = useState("");
  const [lightVMAction, setLightVMAction] = useState("");
  const [runtimeStatus, setRuntimeStatus] = useState<StatusResponse["lightvm"] | null>(null);
  const [dialog, setDialog] = useState<{
    kind: LightVMArtifactKind;
    artifact: LightVMArtifact | null;
  } | null>(null);
  const [lightVMDialog, setLightVMDialog] = useState<{ vm: LightVM | null } | null>(null);
  const kernelLabels = useEntityLabels("lightvm_kernel");
  const initramfsLabels = useEntityLabels("lightvm_initramfs");
  const lightVMLabels = useEntityLabels("lightvm");
  const route = useRoute();

  const publishKernels = (list: LightVMKernel[]) => {
    setKernels(list);
    window.dispatchEvent(new CustomEvent("rookery:lightvm-kernels-changed", { detail: list }));
  };

  const publishInitramfs = (list: LightVMInitramfs[]) => {
    setInitramfs(list);
    window.dispatchEvent(
      new CustomEvent("rookery:lightvm-initramfs-changed", { detail: list })
    );
  };

  const publishLightVMs = (list: LightVM[]) => {
    setLightVMs(list);
    window.dispatchEvent(new CustomEvent("rookery:lightvm-instances-changed", { detail: list }));
  };

  const loadKernels = async (showErrors = true) => {
    try {
      const data = await api<LightVMKernel[]>("/api/lightvm/kernels");
      publishKernels(sortArtifacts(Array.isArray(data) ? data : []));
      if (showErrors) {
        setKernelError("");
      }
    } catch (err) {
      if (showErrors) {
        setKernelError(err instanceof Error ? err.message : String(err));
      }
    }
  };

  const loadInitramfs = async (showErrors = true) => {
    try {
      const data = await api<LightVMInitramfs[]>("/api/lightvm/initramfs");
      publishInitramfs(sortArtifacts(Array.isArray(data) ? data : []));
      if (showErrors) {
        setInitramfsError("");
      }
    } catch (err) {
      if (showErrors) {
        setInitramfsError(err instanceof Error ? err.message : String(err));
      }
    }
  };

  const loadLightVMs = async (showErrors = true) => {
    try {
      const data = await api<LightVM[]>("/api/lightvm/instances");
      publishLightVMs(sortArtifacts(Array.isArray(data) ? data : []));
      if (showErrors) {
        setLightVMError("");
      }
    } catch (err) {
      if (showErrors) {
        setLightVMError(err instanceof Error ? err.message : String(err));
      }
    }
  };

  const loadInterfaces = async () => {
    try {
      const data = await api<VirtualInterface[]>("/api/interfaces");
      setInterfaces(Array.isArray(data) ? data : []);
    } catch {
      setInterfaces([]);
    }
  };

  const loadRuntimeStatus = async () => {
    try {
      const status = await api<StatusResponse>("/api/status");
      setRuntimeStatus(status.lightvm ?? null);
    } catch {
      setRuntimeStatus(null);
    }
  };

  const refresh = async () => {
    setLoading(true);
    await Promise.allSettled([
      loadKernels(),
      loadInitramfs(),
      loadLightVMs(),
      loadInterfaces(),
      loadRuntimeStatus(),
    ]);
    setLoading(false);
  };

  useEffect(() => {
    void refresh();

    const onTabChanged = (event: Event) => {
      if ((event as CustomEvent<string>).detail === "lightvm") {
        void refresh();
      }
    };
    const onAddKernel = () => navigate("/lightvm/kernels/new");
    const onAddInitramfs = () => navigate("/lightvm/initramfs/new");
    const onAddLightVM = () => navigate("/lightvm/instances/new");

    window.addEventListener("rookery:tab-changed", onTabChanged);
    window.addEventListener("rookery:add-lightvm-kernel", onAddKernel);
    window.addEventListener("rookery:add-lightvm-initramfs", onAddInitramfs);
    window.addEventListener("rookery:add-lightvm-instance", onAddLightVM);
    return () => {
      window.removeEventListener("rookery:tab-changed", onTabChanged);
      window.removeEventListener("rookery:add-lightvm-kernel", onAddKernel);
      window.removeEventListener("rookery:add-lightvm-initramfs", onAddInitramfs);
      window.removeEventListener("rookery:add-lightvm-instance", onAddLightVM);
    };
    // The listeners use only state setters and stable API helpers.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    const [root, second, third, fourth] = route.segments;
    if (root !== "lightvm") {
      setDialog(null);
      setLightVMDialog(null);
      return;
    }

    if (second && SECTIONS.some((item) => item.id === second)) {
      setSection(second as Section);
    } else {
      setSection("kernels");
    }

    if (second === "instances") {
      setDialog(null);
      if (third === "new") {
        setLightVMDialog({ vm: null });
        return;
      }
      if (third && fourth === "edit") {
        const vm = lightvms.find((item) => item.id === third);
        if (vm) {
          setLightVMDialog({ vm });
          return;
        }
      }
      setLightVMDialog(null);
      return;
    }

    setLightVMDialog(null);
    if ((second === "kernels" || second === "initramfs") && third === "new") {
      setDialog({
        kind: second === "kernels" ? "kernel" : "initramfs",
        artifact: null,
      });
      return;
    }
    if (
      third &&
      fourth === "edit" &&
      (second === "kernels" || second === "initramfs")
    ) {
      const items: LightVMArtifact[] = second === "kernels" ? kernels : initramfs;
      const artifact = items.find((item) => String(item.id) === third);
      if (artifact) {
        setDialog({
          kind: second === "kernels" ? "kernel" : "initramfs",
          artifact,
        });
        return;
      }
    }
    setDialog(null);
  }, [route.path, kernels, initramfs, lightvms]);

  useEffect(() => {
    const count = document.getElementById("lightvm-tab-count");
    if (count) {
      count.textContent = String(lightvms.length);
    }
  }, [lightvms.length]);

  const removeArtifact = async (
    kind: LightVMArtifactKind,
    artifact: LightVMArtifact
  ) => {
    const noun = kind === "kernel" ? "kernel" : "initramfs";
    const detail = artifact.managed
      ? "The uploaded file is deleted from the Rookery host."
      : "The file on the host is left in place.";
    if (!window.confirm(`Delete ${noun} "${artifact.name}"?\n\n${detail}`)) {
      return;
    }
    try {
      await api(`${endpointFor(kind)}/${artifact.id}`, { method: "DELETE" });
      if (kind === "kernel") {
        publishKernels(kernels.filter((item) => item.id !== artifact.id));
        setKernelError("");
      } else {
        publishInitramfs(initramfs.filter((item) => item.id !== artifact.id));
        setInitramfsError("");
      }
      showToast(`Deleted ${noun} ${artifact.name}`);
    } catch (err) {
      const message = `Could not delete ${artifact.name}: ${
        err instanceof Error ? err.message : String(err)
      }`;
      if (kind === "kernel") {
        setKernelError(message);
      } else {
        setInitramfsError(message);
      }
    }
  };

  const startLightVM = async (vm: LightVM) => {
    const action = `start:${vm.id}`;
    setLightVMAction(action);
    setLightVMError("");
    try {
      const updated = await api<LightVM>(`/api/lightvm/instances/${vm.id}/start`, {
        method: "POST",
      });
      publishLightVMs(lightvms.map((item) => (item.id === updated.id ? updated : item)));
      showToast(`Started LightVM ${updated.name}`);
    } catch (err) {
      setLightVMError(
        `Could not start ${vm.name}: ${err instanceof Error ? err.message : String(err)}`
      );
      void loadLightVMs(false);
    } finally {
      setLightVMAction("");
    }
  };

  const stopLightVM = async (vm: LightVM) => {    const action = `stop:${vm.id}`;
    setLightVMAction(action);
    setLightVMError("");
    try {
      const updated = await api<LightVM>(`/api/lightvm/instances/${vm.id}/stop`, {
        method: "POST",
      });
      publishLightVMs(lightvms.map((item) => (item.id === updated.id ? updated : item)));
      showToast(`Stopped LightVM ${updated.name}`);
    } catch (err) {
      setLightVMError(
        `Could not stop ${vm.name}: ${err instanceof Error ? err.message : String(err)}`
      );
      void loadLightVMs(false);
    } finally {
      setLightVMAction("");
    }
  };

  const openLightVMConsole = (vm: LightVM) => {
    if (!vm.console_enabled) {
      showToast("Console access is disabled. Edit the LightVM to enable it.", true);
      return;
    }
    if (vm.status !== "running") {
      showToast("Start the LightVM before opening its console.", true);
      return;
    }
    window.open(
      `/lightvm-console.html?vm=${encodeURIComponent(vm.id)}`,
      "_blank",
      "noopener"
    );
  };

  const removeLightVM = async (vm: LightVM) => {    if (
      !window.confirm(
        `Delete LightVM "${vm.name}"?\n\nThe definition is removed and its network interfaces are released. No workload is running.`
      )
    ) {
      return;
    }
    try {
      await api(`/api/lightvm/instances/${vm.id}`, { method: "DELETE" });
      publishLightVMs(lightvms.filter((item) => item.id !== vm.id));
      void loadInterfaces();
      setLightVMError("");
      showToast(`Deleted LightVM ${vm.name}`);
    } catch (err) {
      setLightVMError(
        `Could not delete ${vm.name}: ${err instanceof Error ? err.message : String(err)}`
      );
    }
  };

  const kernelSummary =
    kernels.length === 0
      ? loading
        ? "Loading kernels…"
        : "No LightVM kernels uploaded"
      : `${kernels.length} kernel${kernels.length === 1 ? "" : "s"}`;

  const initramfsSummary =
    initramfs.length === 0
      ? loading
        ? "Loading initramfs images…"
        : "No LightVM initramfs uploaded"
      : `${initramfs.length} initramfs image${initramfs.length === 1 ? "" : "s"}`;

  const lightVMSummary =
    lightvms.length === 0
      ? loading
        ? "Loading LightVMs…"
        : "No LightVMs defined"
      : `${lightvms.length} LightVM${lightvms.length === 1 ? "" : "s"} · ${
          lightvms.filter((vm) => vm.status === "running").length
        } running`;
  const runtimeReady = runtimeStatus?.ready !== false;

  return (
    <>
      {runtimeStatus && !runtimeStatus.ready ? (
        <div class="banner" role="alert">
          <strong>LightVM runtime unavailable.</strong>{" "}
          {runtimeStatus.detail ||
            "Install buildah and virtiofsd before starting a LightVM."}
        </div>
      ) : null}

      <div class="subtabs" role="tablist" aria-label="LightVM sections">
        {SECTIONS.map((item) => (
          <button
            key={item.id}
            class="subtab"
            type="button"
            role="tab"
            aria-selected={section === item.id}
            onClick={() => navigate(`/lightvm/${item.id}`)}
          >
            {item.label}
          </button>
        ))}
      </div>

      <section class="panel" aria-labelledby="kernels-heading" hidden={section !== "kernels"}>
        <div class="panel-head">
          <div>
            <h2 id="kernels-heading">LightVM kernels</h2>
            <p class="panel-subtitle">{kernelSummary}</p>
          </div>
          <div class="actions">
            <button class="button ghost" type="button" disabled={loading} onClick={() => void refresh()}>
              {loading ? "Refreshing…" : "Refresh"}
            </button>
            <button
              class="button primary"
              type="button"
              onClick={() => navigate("/lightvm/kernels/new")}
            >
              <span class="button-plus" aria-hidden="true">+</span>
              Add kernel
            </button>
          </div>
        </div>

        <p class="field-hint">
          Kernel images are uploaded to the host and paired with an initramfs when a LightVM is
          created. Names are unique; labels are managed by the shared label editor.
        </p>

        {kernelError ? (
          <div class="banner" role="alert">
            {kernelError}
          </div>
        ) : null}

        <div class="table-wrap">
          {kernels.length > 0 ? (
            <table>
              <thead>
                <tr>
                  <th scope="col">Name</th>
                  <th scope="col">File</th>
                  <th scope="col">Size</th>
                  <th scope="col">Source</th>
                  <th scope="col" class="hide-sm">Updated</th>
                  <th scope="col">
                    <span class="sr-only">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {kernels.map((kernel) => (
                  <tr key={kernel.id}>
                    <td>
                      <span class="vm-name">{kernel.name}</span>
                      {kernel.description ? (
                        <span class="vm-description" title={kernel.description}>
                          {kernel.description}
                        </span>
                      ) : null}
                      <EntityLabels
                        entityType="lightvm_kernel"
                        entityID={kernel.id}
                        name={kernel.name}
                        labels={kernelLabels.labels[String(kernel.id)]}
                        onSaved={(labels) =>
                          kernelLabels.setEntityLabels(String(kernel.id), labels)
                        }
                      />
                    </td>
                    <td>
                      <span class="vm-name mono">{kernel.file_name || "—"}</span>
                      <span class="vm-description mono" title={kernel.path}>
                        {kernel.path}
                      </span>
                    </td>
                    <td class="mono">{formatBytes(kernel.size_bytes)}</td>
                    <td class="mono">{kernel.managed ? "uploaded" : "host path"}</td>
                    <td class="updated hide-sm">{formatDate(kernel.updated_at)}</td>
                    <td>
                      <div class="actions">
                        <button
                          class="row-action"
                          type="button"
                          onClick={() => navigate(`/lightvm/kernels/${kernel.id}/edit`)}
                        >
                          Edit
                        </button>
                        <button
                          class="row-action danger"
                          type="button"
                          onClick={() => void removeArtifact("kernel", kernel)}
                        >
                          Delete
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : (
            <div class="empty">
              <div class="empty-mark" aria-hidden="true">
                ⌁
              </div>
              <h3>No LightVM kernels yet</h3>
              <p>Upload a kernel image to use as the boot kernel for LightVM instances.</p>
              <button
                class="button primary"
                type="button"
                onClick={() => navigate("/lightvm/kernels/new")}
              >
                Add kernel
              </button>
            </div>
          )}
        </div>
      </section>

      <section class="panel panel-spaced" aria-labelledby="initramfs-heading" hidden={section !== "initramfs"}>
        <div class="panel-head">
          <div>
            <h2 id="initramfs-heading">LightVM initramfs</h2>
            <p class="panel-subtitle">{initramfsSummary}</p>
          </div>
          <div class="actions">
            <button class="button ghost" type="button" disabled={loading} onClick={() => void refresh()}>
              {loading ? "Refreshing…" : "Refresh"}
            </button>
            <button
              class="button primary"
              type="button"
              onClick={() => navigate("/lightvm/initramfs/new")}
            >
              <span class="button-plus" aria-hidden="true">+</span>
              Add initramfs
            </button>
          </div>
        </div>

        <p class="field-hint">
          Initramfs images are uploaded to the host and paired with a kernel when a LightVM is
          created. Names are unique; labels are managed by the shared label editor.
        </p>

        {initramfsError ? (
          <div class="banner" role="alert">
            {initramfsError}
          </div>
        ) : null}

        <div class="table-wrap">
          {initramfs.length > 0 ? (
            <table>
              <thead>
                <tr>
                  <th scope="col">Name</th>
                  <th scope="col">File</th>
                  <th scope="col">Size</th>
                  <th scope="col">Source</th>
                  <th scope="col" class="hide-sm">Updated</th>
                  <th scope="col">
                    <span class="sr-only">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {initramfs.map((image) => (
                  <tr key={image.id}>
                    <td>
                      <span class="vm-name">{image.name}</span>
                      {image.description ? (
                        <span class="vm-description" title={image.description}>
                          {image.description}
                        </span>
                      ) : null}
                      <EntityLabels
                        entityType="lightvm_initramfs"
                        entityID={image.id}
                        name={image.name}
                        labels={initramfsLabels.labels[String(image.id)]}
                        onSaved={(labels) =>
                          initramfsLabels.setEntityLabels(String(image.id), labels)
                        }
                      />
                    </td>
                    <td>
                      <span class="vm-name mono">{image.file_name || "—"}</span>
                      <span class="vm-description mono" title={image.path}>
                        {image.path}
                      </span>
                    </td>
                    <td class="mono">{formatBytes(image.size_bytes)}</td>
                    <td class="mono">{image.managed ? "uploaded" : "host path"}</td>
                    <td class="updated hide-sm">{formatDate(image.updated_at)}</td>
                    <td>
                      <div class="actions">
                        <button
                          class="row-action"
                          type="button"
                          onClick={() => navigate(`/lightvm/initramfs/${image.id}/edit`)}
                        >
                          Edit
                        </button>
                        <button
                          class="row-action danger"
                          type="button"
                          onClick={() => void removeArtifact("initramfs", image)}
                        >
                          Delete
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : (
            <div class="empty">
              <div class="empty-mark" aria-hidden="true">
                ◌
              </div>
              <h3>No LightVM initramfs yet</h3>
              <p>Upload an initramfs image to pair with a kernel for LightVM instances.</p>
              <button
                class="button primary"
                type="button"
                onClick={() => navigate("/lightvm/initramfs/new")}
              >
                Add initramfs
              </button>
            </div>
          )}
        </div>
      </section>

      <section class="panel panel-spaced" aria-labelledby="lightvms-heading" hidden={section !== "instances"}>
        <div class="panel-head">
          <div>
            <h2 id="lightvms-heading">LightVMs</h2>
            <p class="panel-subtitle">{lightVMSummary}</p>
          </div>
          <div class="actions">
            <button class="button ghost" type="button" disabled={loading} onClick={() => void refresh()}>
              {loading ? "Refreshing…" : "Refresh"}
            </button>
            <button
              class="button primary"
              type="button"
              disabled={kernels.length === 0 || initramfs.length === 0}
              title={
                kernels.length === 0
                  ? "Upload a kernel first."
                  : initramfs.length === 0
                    ? "Upload an initramfs first."
                    : ""
              }
              onClick={() => navigate("/lightvm/instances/new")}
            >
              <span class="button-plus" aria-hidden="true">+</span>
              Add LightVM
            </button>
          </div>
        </div>

        <p class="field-hint">
          A LightVM combines uploaded kernel and initramfs images with a container image, environment,
          executable path, and arguments. Definitions are inventory-only until the runtime is added.
        </p>

        {lightVMError ? (
          <div class="banner" role="alert">
            {lightVMError}
          </div>
        ) : null}

        <div class="table-wrap">
          {lightvms.length > 0 ? (
            <table>
              <thead>
                <tr>
                  <th scope="col">Name</th>
                  <th scope="col">Status</th>
                  <th scope="col">Resources</th>
                  <th scope="col">Boot images</th>
                  <th scope="col">Container image</th>
                  <th scope="col">Workload</th>
                  <th scope="col">Interfaces</th>
                  <th scope="col" class="hide-sm">Updated</th>
                  <th scope="col">
                    <span class="sr-only">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {lightvms.map((vm) => {
                  const args = Array.isArray(vm.executable_args) ? vm.executable_args : [];
                  const env = Array.isArray(vm.env) ? vm.env : [];
                  const attached = Array.isArray(vm.interfaces) ? vm.interfaces : [];
                  const running = vm.status === "running" || vm.status === "starting";
                  const starting = lightVMAction === `start:${vm.id}`;
                  const stopping = lightVMAction === `stop:${vm.id}`;
                  return (
                    <tr key={vm.id}>
                      <td>
                        <span class="vm-name">{vm.name}</span>
                        {vm.description ? (
                          <span class="vm-description" title={vm.description}>
                            {vm.description}
                          </span>
                        ) : null}
                        <EntityLabels
                          entityType="lightvm"
                          entityID={vm.id}
                          name={vm.name}
                          labels={lightVMLabels.labels[vm.id]}
                          onSaved={(labels) => lightVMLabels.setEntityLabels(vm.id, labels)}
                        />
                      </td>
                      <td>
                        <span class="status-badge" data-status={vm.status}>
                          {vm.status || "unknown"}
                        </span>
                        {vm.console_socket ? (
                          <span class="vm-description mono" title={vm.console_socket}>
                            {vm.console_socket}
                          </span>
                        ) : null}
                      </td>
                      <td class="mono">
                        <span>{vm.vcpus} vCPU · {vm.memory_mb} MB</span>
                        {vm.console_enabled ? <span class="vm-description">console enabled</span> : null}
                        {vm.cgroup_class ? (
                          <span class="vm-description">cgroup {vm.cgroup_class}</span>
                        ) : null}
                      </td>
                      <td>
                        <span class="vm-name">{vm.kernel_name || "—"}</span>
                        <span class="vm-description">{vm.initramfs_name || "—"}</span>
                        <span class="vm-description mono" title={vm.append}>
                          {vm.append ? `append: ${vm.append}` : "append: none"}
                        </span>
                      </td>
                      <td class="mono">{vm.image}</td>
                      <td>
                        <span class="vm-name mono" title={vm.executable_path}>
                          {vm.executable_path}
                        </span>
                        <span class="vm-description mono" title={args.join(" ")}>
                          {args.length ? args.join(" ") : "no arguments"}
                        </span>
                        <span class="vm-description">
                          {env.length} env var{env.length === 1 ? "" : "s"}
                        </span>
                      </td>
                      <td class={attached.length ? "mono" : "mono muted"} title={attached.map((item) => item.name).join(", ")}>
                        {attached.length ? attached.map((item) => item.name).join(", ") : "none"}
                      </td>
                      <td class="updated hide-sm">{formatDate(vm.updated_at)}</td>
                      <td>
                        <div class="actions">
                          {running ? (
                            <button
                              class="row-action"
                              type="button"
                              disabled={stopping}
                              onClick={() => void stopLightVM(vm)}
                            >
                              {stopping ? "Stopping…" : "Stop"}
                            </button>
                          ) : (
                            <button
                              class="row-action"
                              type="button"
                              disabled={starting || !runtimeReady}
                              title={
                                !runtimeReady
                                  ? runtimeStatus?.detail ||
                                    "Install buildah and virtiofsd before starting a LightVM."
                                  : ""
                              }
                              onClick={() => void startLightVM(vm)}
                            >
                              {starting ? "Starting…" : "Start"}
                            </button>
                          )}
                          <button
                            class="row-action"
                            type="button"
                            disabled={!vm.console_enabled || vm.status !== "running"}
                            title={
                              !vm.console_enabled
                                ? "Console access is disabled. Edit the LightVM to enable it."
                                : vm.status !== "running"
                                  ? "Start the LightVM before opening its console."
                                  : ""
                            }
                            onClick={() => openLightVMConsole(vm)}
                          >
                            Console
                          </button>
                          <button
                            class="row-action"
                            type="button"
                            disabled={running}
                            title={running ? "Stop the LightVM before editing it." : ""}
                            onClick={() => navigate(`/lightvm/instances/${vm.id}/edit`)}
                          >
                            Edit
                          </button>
                          <button
                            class="row-action danger"
                            type="button"
                            onClick={() => void removeLightVM(vm)}
                          >
                            Delete
                          </button>
                        </div>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          ) : (
            <div class="empty">
              <div class="empty-mark" aria-hidden="true">
                ▢
              </div>
              <h3>No LightVMs yet</h3>
              <p>
                Upload a kernel and initramfs, then define a container-backed LightVM.
              </p>
              <button
                class="button primary"
                type="button"
                disabled={kernels.length === 0 || initramfs.length === 0}
                title={
                  kernels.length === 0
                    ? "Upload a kernel first."
                    : initramfs.length === 0
                      ? "Upload an initramfs first."
                      : ""
                }
                onClick={() => navigate("/lightvm/instances/new")}
              >
                Add LightVM
              </button>
            </div>
          )}
        </div>
      </section>

      {dialog ? (
        <ArtifactDialog
          key={`${dialog.kind}-${dialog.artifact ? dialog.artifact.id : "new"}`}
          kind={dialog.kind}
          artifact={dialog.artifact}
          onClose={() => {
            setDialog(null);
            navigate(`/lightvm/${sectionFor(dialog.kind)}`, true);
          }}
          onSaved={(saved, created) => {
            const kind = dialog.kind;
            setDialog(null);
            navigate(`/lightvm/${sectionFor(kind)}`, true);
            if (kind === "kernel") {
              const kernel = saved as LightVMKernel;
              publishKernels(
                created
                  ? sortArtifacts([...kernels, kernel])
                  : sortArtifacts(
                      kernels.map((item) => (item.id === kernel.id ? kernel : item))
                    )
              );
              setKernelError("");
            } else {
              const image = saved as LightVMInitramfs;
              publishInitramfs(
                created
                  ? sortArtifacts([...initramfs, image])
                  : sortArtifacts(
                      initramfs.map((item) => (item.id === image.id ? image : item))
                    )
              );
              setInitramfsError("");
            }
            const noun = kind === "kernel" ? "kernel" : "initramfs";
            showToast(created ? `Added ${noun} ${saved.name}` : `Saved ${noun} ${saved.name}`);
          }}
        />
      ) : null}

      {lightVMDialog ? (
        <LightVMDialog
          key={lightVMDialog.vm ? lightVMDialog.vm.id : "new"}
          vm={lightVMDialog.vm}
          kernels={kernels}
          initramfs={initramfs}
          interfaces={interfaces}
          onClose={() => {
            setLightVMDialog(null);
            navigate("/lightvm/instances", true);
          }}
          onSaved={(saved) => {
            const created = lightVMDialog.vm === null;
            setLightVMDialog(null);
            navigate("/lightvm/instances", true);
            publishLightVMs(
              created
                ? sortArtifacts([...lightvms, saved])
                : sortArtifacts(lightvms.map((item) => (item.id === saved.id ? saved : item)))
            );
            setLightVMError("");
            void loadInterfaces();
            showToast(created ? `Created LightVM ${saved.name}` : `Saved LightVM ${saved.name}`);
          }}
        />
      ) : null}
    </>
  );
}
