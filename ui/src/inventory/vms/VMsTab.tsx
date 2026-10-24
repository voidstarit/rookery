import { useEffect, useState } from "preact/hooks";
import { api } from "../../shared/api";
import { showToast } from "../../shared/toast";
import type {
  Disk,
  Media,
  StatusResponse,
  VirtualInterface,
  VM,
} from "../types";
import { formatMemory } from "./monitor";
import { VMDialog } from "./VMDialog";
import { VMMonitorDialog } from "./VMMonitorDialog";
import { navigate, useRoute } from "../router";

const numberFormat = new Intl.NumberFormat();

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

function vmDisks(vm: VM) {
  if (Array.isArray(vm.disks) && vm.disks.length > 0) {
    return vm.disks;
  }
  return vm.disk ? [vm.disk] : [];
}

export function VMsTab() {
  const [vms, setVms] = useState<VM[]>([]);
  const [disks, setDisks] = useState<Disk[]>([]);
  const [media, setMedia] = useState<Media[]>([]);
  const [interfaces, setInterfaces] = useState<VirtualInterface[]>([]);
  const [ready, setReady] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [dialog, setDialog] = useState<{ vm: VM | null } | null>(null);
  const [monitorVM, setMonitorVM] = useState<VM | null>(null);
  const route = useRoute();

  useEffect(() => {
    if (route.segments[0] !== "vms") {
      setDialog(null);
      setMonitorVM(null);
      return;
    }
    const action = route.segments[1];
    const mode = route.segments[2];
    if (action === "new") {
      setMonitorVM(null);
      setDialog({ vm: null });
      return;
    }
    if (action && action !== "new") {
      const vm = vms.find((item) => item.id === action);
      if (vm && mode === "edit") {
        setMonitorVM(null);
        setDialog({ vm });
        return;
      }
      if (vm && mode === "monitor") {
        setDialog(null);
        setMonitorVM(vm);
        return;
      }
    }
    setDialog(null);
    setMonitorVM(null);
  }, [route.path, vms]);

  const publishVMs = (list: VM[]) => {
    setVms(list);
    window.dispatchEvent(new CustomEvent("rookery:vms-changed", { detail: list }));
  };

  const loadVMs = async (showErrors = true) => {
    try {
      const data = await api<VM[]>("/api/vms");
      publishVMs(Array.isArray(data) ? data : []);
      if (showErrors) {
        setError("");
      }
    } catch (err) {
      if (showErrors) {
        setError(err instanceof Error ? err.message : String(err));
      }
    }
  };

  const loadDisks = async () => {
    try {
      const data = await api<Disk[]>("/api/disks");
      setDisks(Array.isArray(data) ? data : []);
    } catch {
      setDisks([]);
    }
  };

  const loadMedia = async () => {
    try {
      const data = await api<Media[]>("/api/media");
      setMedia(Array.isArray(data) ? data : []);
    } catch {
      setMedia([]);
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

  const loadStatus = async () => {
    try {
      const status = await api<StatusResponse>("/api/status");
      setReady(Boolean(status.ready));
    } catch {
      setReady(false);
    }
  };

  const refreshAll = async () => {
    setLoading(true);
    await Promise.allSettled([loadVMs(), loadDisks(), loadMedia(), loadInterfaces(), loadStatus()]);
    setLoading(false);
  };

  useEffect(() => {
    void refreshAll();

    const onDisksChanged = (event: Event) => {
      const detail = (event as CustomEvent<Disk[]>).detail;
      if (Array.isArray(detail)) {
        setDisks(detail);
      }
    };
    const onMediaChanged = (event: Event) => {
      const detail = (event as CustomEvent<Media[]>).detail;
      if (Array.isArray(detail)) {
        setMedia(detail);
      }
    };
    const onInterfacesChanged = (event: Event) => {
      const detail = (event as CustomEvent<VirtualInterface[]>).detail;
      if (Array.isArray(detail)) {
        setInterfaces(detail);
      }
    };
    const onReadiness = (event: Event) => {
      const detail = (event as CustomEvent<{ ready: boolean }>).detail;
      if (detail && typeof detail.ready === "boolean") {
        setReady(detail.ready);
      }
    };
    const onTabChanged = (event: Event) => {
      if ((event as CustomEvent<string>).detail === "vms") {
        void refreshAll();
      }
    };
    const onAddVM = () => {
      navigate("/vms/new");
    };

    window.addEventListener("rookery:disks-changed", onDisksChanged);
    window.addEventListener("rookery:media-changed", onMediaChanged);
    window.addEventListener("rookery:interfaces-changed", onInterfacesChanged);
    window.addEventListener("rookery:readiness-changed", onReadiness);
    window.addEventListener("rookery:tab-changed", onTabChanged);
    window.addEventListener("rookery:add-vm", onAddVM);

    return () => {
      window.removeEventListener("rookery:disks-changed", onDisksChanged);
      window.removeEventListener("rookery:media-changed", onMediaChanged);
      window.removeEventListener("rookery:interfaces-changed", onInterfacesChanged);
      window.removeEventListener("rookery:readiness-changed", onReadiness);
      window.removeEventListener("rookery:tab-changed", onTabChanged);
      window.removeEventListener("rookery:add-vm", onAddVM);
    };
    // The listeners use only state setters and stable API helpers.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    const count = `${vms.length} VM${vms.length === 1 ? "" : "s"}`;
    const topCount = document.getElementById("vm-count");
    if (topCount) {
      topCount.textContent = count;
    }
    const tabCount = document.getElementById("vm-tab-count");
    if (tabCount) {
      tabCount.textContent = String(vms.length);
    }
  }, [vms.length]);

  const freeDisks = disks.filter((disk) => !disk.attached_vm_id && !disk.is_template);

  useEffect(() => {
    const newVM = document.getElementById("new-vm") as HTMLButtonElement | null;
    const hint = document.getElementById("new-vm-hint");
    if (!newVM || !hint) {
      return;
    }
    let message = "";
    if (!ready) {
      message = "VM creation is blocked until the thin pool and FD.io VPP are ready.";
    } else if (freeDisks.length === 0) {
      message = "Create an unattached disk on the Disks tab first.";
    }
    newVM.disabled = message !== "";
    newVM.title = message;
    hint.textContent = message;
    hint.hidden = message === "";
  }, [ready, freeDisks.length]);

  const openCreate = () => {
    if (!ready) {
      showToast("VM creation is blocked until the thin pool and FD.io VPP are ready.", true);
      return;
    }
    if (freeDisks.length === 0) {
      showToast("Create an unattached disk on the Disks tab first.", true);
      return;
    }
    setDialog({ vm: null });
    navigate("/vms/new");
  };

  const deleteVM = async (vm: VM) => {
    if (!window.confirm(`Delete "${vm.name}"?\n\nThis removes the inventory record only. The disk remains available.`)) {
      return;
    }
    try {
      await api(`/api/vms/${vm.id}`, { method: "DELETE" });
      setError("");
      showToast(`Deleted ${vm.name}`);
      await Promise.allSettled([loadVMs(), loadDisks(), loadInterfaces()]);
    } catch (err) {
      setError(`Could not delete ${vm.name}: ${err instanceof Error ? err.message : String(err)}`);
    }
  };

  const startVM = async (vm: VM) => {
    try {
      const updated = await api<VM>(`/api/vms/${vm.id}/start`, { method: "POST" });
      publishVMs(vms.map((item) => (item.id === updated.id ? updated : item)));
      setError("");
      showToast(`Started ${vm.name}`);
      void loadDisks();
    } catch (err) {
      setError(`Could not start ${vm.name}: ${err instanceof Error ? err.message : String(err)}`);
      void loadVMs();
    }
  };

  const stopVM = async (vm: VM) => {
    try {
      const updated = await api<VM>(`/api/vms/${vm.id}/stop`, { method: "POST" });
      publishVMs(vms.map((item) => (item.id === updated.id ? updated : item)));
      setError("");
      showToast(`Stopped ${vm.name}`);
      void loadDisks();
    } catch (err) {
      setError(`Could not stop ${vm.name}: ${err instanceof Error ? err.message : String(err)}`);
      void loadVMs();
    }
  };

  const openConsole = (vm: VM) => {
    if (!vm.console_enabled) {
      showToast("Console access is disabled. Edit the VM to enable it.", true);
      return;
    }
    if (vm.status !== "running") {
      showToast("Start the VM before opening its console.", true);
      return;
    }
    window.open(`/console.html?vm=${encodeURIComponent(vm.id)}`, "_blank", "noopener");
  };

  const running = vms.filter((vm) => vm.status === "running").length;
  const summary =
    vms.length === 0
      ? loading
        ? "Loading instances…"
        : "No instances registered"
      : `${vms.length} instance${vms.length === 1 ? "" : "s"} · ${running} running`;

  return (
    <>
      <section class="panel" aria-labelledby="inventory-heading">
        <div class="panel-head">
          <div>
            <h2 id="inventory-heading">Inventory</h2>
            <p class="panel-subtitle">{summary}</p>
          </div>
          <button class="button ghost" type="button" disabled={loading} onClick={() => void refreshAll()}>
            {loading ? "Refreshing…" : "Refresh"}
          </button>
        </div>

        {error ? (
          <div class="banner" role="alert">
            {error}
          </div>
        ) : null}

        <div class="table-wrap">
          {vms.length > 0 ? (
            <table>
              <thead>
                <tr>
                  <th scope="col">Name</th>
                  <th scope="col">Status</th>
                  <th scope="col">vCPU</th>
                  <th scope="col">Memory</th>
                  <th scope="col">Disks</th>
                  <th scope="col">Boot</th>
                  <th scope="col">Console</th>
                  <th scope="col">Network</th>
                  <th scope="col" class="hide-sm">
                    Updated
                  </th>
                  <th scope="col">
                    <span class="sr-only">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {vms.map((vm) => {
                  const disksForVM = vmDisks(vm);
                  const bootMedia = media.find((item) => item.id === vm.boot_media_id) || vm.boot_media;
                  const vmInterfaces = Array.isArray(vm.interfaces) ? vm.interfaces : [];
                  const active = vm.status === "running" || vm.status === "starting";
                  return (
                    <tr key={vm.id}>
                      <td>
                        <span class="vm-name">{vm.name}</span>
                        {vm.description ? (
                          <span class="vm-description" title={vm.description}>
                            {vm.description}
                          </span>
                        ) : null}
                      </td>
                      <td>
                        <span class="status-badge" data-status={vm.status}>
                          {vm.status}
                        </span>
                      </td>
                      <td class="mono">{numberFormat.format(vm.vcpus)}</td>
                      <td class="mono">{formatMemory(vm.memory_mb)}</td>
                      <td
                        class={disksForVM.length ? "mono" : "mono muted"}
                        title={
                          disksForVM.length
                            ? disksForVM
                                .map((disk) => `${disk.name} · ${numberFormat.format(disk.size_gb)} GB`)
                                .join(", ")
                            : undefined
                        }
                      >
                        {disksForVM.length ? disksForVM.map((disk) => disk.name).join(", ") : "—"}
                      </td>
                      <td class={bootMedia ? "mono" : "mono muted"} title={bootMedia?.path || undefined}>
                        {bootMedia ? bootMedia.name : "disk"}
                      </td>
                      <td class={vm.console_enabled ? "mono" : "mono muted"}>
                        {vm.console_enabled ? "enabled" : "disabled"}
                      </td>
                      <td
                        class={vmInterfaces.length ? "mono" : "mono muted"}
                        title={
                          vmInterfaces.length
                            ? vmInterfaces
                                .map((iface) => `${iface.name} (${iface.network_name})`)
                                .join(", ")
                            : undefined
                        }
                      >
                        {vmInterfaces.length
                          ? vmInterfaces.map((iface) => iface.network_name || iface.name).join(", ")
                          : "none"}
                      </td>
                      <td class="updated hide-sm">{formatDate(vm.updated_at)}</td>
                      <td>
                        <div class="actions">
                          <button
                            class={active ? "row-action danger" : "row-action"}
                            type="button"
                            onClick={() => (active ? void stopVM(vm) : void startVM(vm))}
                          >
                            {active ? "Stop" : "Start"}
                          </button>
                          <button
                            class="row-action"
                            type="button"
                            disabled={!vm.console_enabled || vm.status !== "running"}
                            title={
                              !vm.console_enabled
                                ? "Console access is disabled. Edit the VM to enable it."
                                : vm.status !== "running"
                                  ? "Start the VM to open its console."
                                  : ""
                            }
                            onClick={() => openConsole(vm)}
                          >
                            Console
                          </button>
                          <button class="row-action" type="button" onClick={() => navigate(`/vms/${vm.id}/monitor`)}>
                            Monitor
                          </button>
                          <button class="row-action" type="button" onClick={() => navigate(`/vms/${vm.id}/edit`)}>
                            Edit
                          </button>
                          <button class="row-action danger" type="button" onClick={() => void deleteVM(vm)}>
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
                ⬡
              </div>
              <h3>No virtual machines yet</h3>
              <p>Create the first instance to start building out this host.</p>
              <button
                class="button primary"
                type="button"
                disabled={!ready || freeDisks.length === 0}
                title={
                  !ready
                    ? "VM creation is blocked until the thin pool and FD.io VPP are ready."
                    : freeDisks.length === 0
                      ? "Create an unattached disk on the Disks tab first."
                      : ""
                }
                onClick={openCreate}
              >
                Create a VM
              </button>
            </div>
          )}
        </div>
      </section>

      {dialog ? (
        <VMDialog
          key={dialog.vm ? `edit-${dialog.vm.id}` : "new"}
          vm={dialog.vm}
          disks={disks}
          media={media}
          interfaces={interfaces}
          ready={ready}
          onClose={() => {
            setDialog(null);
            navigate("/vms", true);
          }}
          onSaved={(saved) => {
            setDialog(null);
            navigate("/vms", true);
            setError("");
            showToast(`${dialog.vm ? "Saved" : "Created"} ${saved.name}`);
            void Promise.allSettled([loadVMs(), loadDisks(), loadInterfaces()]);
          }}
        />
      ) : null}

      {monitorVM ? (
        <VMMonitorDialog
          key={`monitor-${monitorVM.id}`}
          vm={monitorVM}
          onClose={() => {
            setMonitorVM(null);
            navigate("/vms", true);
          }}
        />
      ) : null}
    </>
  );
}
