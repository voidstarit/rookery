import { useEffect, useState } from "preact/hooks";
import { api } from "../../shared/api";
import { showToast } from "../../shared/toast";
import type { Disk, StorageBackend } from "../types";
import { DiskDialog } from "./DiskDialog";
import { ImportDisksDialog } from "./ImportDisksDialog";
import { StorageBackendDialog } from "./StorageBackendDialog";
import { TemplateProvisioningDialog } from "./TemplateProvisioningDialog";
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

export function DisksTab() {
  const [disks, setDisks] = useState<Disk[]>([]);
  const [backends, setBackends] = useState<StorageBackend[]>([]);
  const [loading, setLoading] = useState(true);
  const [diskError, setDiskError] = useState("");
  const [backendError, setBackendError] = useState("");
  const [diskDialogOpen, setDiskDialogOpen] = useState(false);
  const [backendDialog, setBackendDialog] = useState<{ backend: StorageBackend | null } | null>(null);
  const [importBackend, setImportBackend] = useState<StorageBackend | null>(null);
  const [provisioningDisk, setProvisioningDisk] = useState<Disk | null>(null);
  const route = useRoute();

  useEffect(() => {
    setProvisioningDisk(null);
  }, [route.path, route.search]);

  useEffect(() => {
    const [section, second, third] = route.segments;
    if (section === "disks") {
      setDiskDialogOpen(second === "new");
      setBackendDialog(null);
      setImportBackend(null);
      return;
    }
    if (section === "storage-backends") {
      setDiskDialogOpen(false);
      if (second === "new") {
        setBackendDialog({ backend: null });
        setImportBackend(null);
        return;
      }
      const backend = backends.find((item) => String(item.id) === second);
      if (backend) {
        setBackendDialog(third === "edit" ? { backend } : null);
        setImportBackend(third === "import" ? backend : null);
      } else {
        setBackendDialog(null);
        setImportBackend(null);
      }
      return;
    }
    setDiskDialogOpen(false);
    setBackendDialog(null);
    setImportBackend(null);
  }, [route.path, backends]);

  const publishDisks = (list: Disk[]) => {
    setDisks(list);
    window.dispatchEvent(new CustomEvent("rookery:disks-changed", { detail: list }));
  };

  const loadDisks = async (showErrors = true) => {
    try {
      const data = await api<Disk[]>("/api/disks");
      publishDisks(Array.isArray(data) ? data : []);
      if (showErrors) {
        setDiskError("");
      }
    } catch (err) {
      if (showErrors) {
        setDiskError(err instanceof Error ? err.message : String(err));
      }
    }
  };

  const loadBackends = async (showErrors = true) => {
    try {
      const data = await api<StorageBackend[]>("/api/storage-backends");
      const list = Array.isArray(data) ? data : [];
      list.sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: "base" }));
      setBackends(list);
      if (showErrors) {
        setBackendError("");
      }
    } catch (err) {
      if (showErrors) {
        setBackendError(err instanceof Error ? err.message : String(err));
      }
    }
  };

  const refreshAll = async () => {
    setLoading(true);
    await Promise.allSettled([loadDisks(), loadBackends()]);
    setLoading(false);
  };

  useEffect(() => {
    void refreshAll();

    const onDisksData = (event: Event) => {
      const detail = (event as CustomEvent<Disk[]>).detail;
      if (Array.isArray(detail)) {
        setDisks(detail);
      }
    };
    const onAddDisk = () => {
      navigate("/disks/new");
    };
    const onTabChanged = (event: Event) => {
      if ((event as CustomEvent<string>).detail === "disks") {
        void refreshAll();
      }
    };

    window.addEventListener("rookery:disks-data", onDisksData);
    window.addEventListener("rookery:add-disk", onAddDisk);
    window.addEventListener("rookery:tab-changed", onTabChanged);

    return () => {
      window.removeEventListener("rookery:disks-data", onDisksData);
      window.removeEventListener("rookery:add-disk", onAddDisk);
      window.removeEventListener("rookery:tab-changed", onTabChanged);
    };
    // The listeners use only state setters and stable API helpers.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    const count = document.getElementById("disk-tab-count");
    if (count) {
      count.textContent = String(disks.length);
    }
  }, [disks.length]);

  useEffect(() => {
    const newDisk = document.getElementById("new-disk") as HTMLButtonElement | null;
    if (!newDisk) {
      return;
    }
    const blocked = backends.length === 0;
    newDisk.disabled = blocked;
    newDisk.title = blocked ? "Add a storage backend before creating disks" : "";
  }, [backends.length]);

  const deleteDisk = async (disk: Disk) => {
    if (disk.attached_vm_id) {
      showToast(`Detach ${disk.name} from ${disk.attached_vm_name || "its VM"} before deleting it.`, true);
      return;
    }
    const confirmed = window.confirm(
      `Delete disk "${disk.name}"?\n\nThis removes the LVM thin volume and its inventory record.`
    );
    if (!confirmed) {
      return;
    }
    try {
      await api(`/api/disks/${disk.id}`, { method: "DELETE" });
      publishDisks(disks.filter((item) => item.id !== disk.id));
      setDiskError("");
      showToast(`Deleted disk ${disk.name}`);
      void loadBackends(false);
    } catch (err) {
      setDiskError(`Could not delete ${disk.name}: ${err instanceof Error ? err.message : String(err)}`);
    }
  };

  const toggleTemplate = async (disk: Disk) => {
    if (disk.attached_vm_id) {
      showToast("Detach the disk from its VM before changing template status.", true);
      return;
    }
    try {
      const updated = await api<Disk>(`/api/disks/${disk.id}`, {
        method: "PUT",
        body: JSON.stringify({ is_template: !disk.is_template }),
      });
      publishDisks(disks.map((item) => (item.id === updated.id ? updated : item)));
      showToast(updated.is_template ? `Marked ${updated.name} as a template` : `Unmarked ${updated.name} as a template`);
    } catch (err) {
      setDiskError(
        `Could not update ${disk.name}: ${err instanceof Error ? err.message : String(err)}`
      );
    }
  };

  const deleteBackend = async (backend: StorageBackend) => {
    if (backend.disk_count > 0) {
      showToast("Delete disks using this backend first.", true);
      return;
    }
    const confirmed = window.confirm(`Delete storage backend "${backend.name}"?`);
    if (!confirmed) {
      return;
    }
    try {
      await api(`/api/storage-backends/${backend.id}`, { method: "DELETE" });
      setBackends(backends.filter((item) => item.id !== backend.id));
      setBackendError("");
      showToast(`Deleted storage backend ${backend.name}`);
    } catch (err) {
      setBackendError(
        `Could not delete ${backend.name}: ${err instanceof Error ? err.message : String(err)}`
      );
    }
  };

  const attached = disks.filter((disk) => disk.attached_vm_id).length;
  const diskSummary =
    disks.length === 0
      ? loading
        ? "Loading disks…"
        : "No disks registered"
      : `${disks.length} disk${disks.length === 1 ? "" : "s"} · ${attached} attached`;
  const totalBackendDisks = backends.reduce((sum, backend) => sum + (backend.disk_count || 0), 0);
  const backendSummary =
    backends.length === 0
      ? loading
        ? "Loading storage backends…"
        : "No storage backends registered"
      : `${backends.length} backend${backends.length === 1 ? "" : "s"} · ${totalBackendDisks} disk${totalBackendDisks === 1 ? "" : "s"}`;

  return (
    <>
      <section class="panel" aria-labelledby="storage-backends-heading">
        <div class="panel-head">
          <div>
            <h2 id="storage-backends-heading">Storage backends</h2>
            <p class="panel-subtitle">{backendSummary}</p>
          </div>
          <div class="actions">
            <button class="button ghost" type="button" disabled={loading} onClick={() => void refreshAll()}>
              {loading ? "Refreshing…" : "Refresh"}
            </button>
            <button class="button primary" type="button" onClick={() => navigate("/storage-backends/new")}>
              <span class="button-plus" aria-hidden="true">
                +
              </span>
              Add backend
            </button>
          </div>
        </div>

        {backendError ? (
          <div class="banner" role="alert">
            {backendError}
          </div>
        ) : null}

        <div class="table-wrap">
          {backends.length > 0 ? (
            <table>
              <thead>
                <tr>
                  <th scope="col">Name</th>
                  <th scope="col">Driver</th>
                  <th scope="col">Location</th>
                  <th scope="col">Disks</th>
                  <th scope="col" class="hide-sm">
                    Updated
                  </th>
                  <th scope="col">
                    <span class="sr-only">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {backends.map((backend) => {
                  const importAvailable =
                    backend.driver === "lvm-thin" && backend.config?.vg && backend.config?.thin_pool;
                  return (
                    <tr key={backend.id}>
                      <td>
                        <span class="vm-name">{backend.name}</span>
                        {backend.title || backend.description ? (
                          <span class="vm-description" title={backend.description || backend.title || ""}>
                            {[backend.title, backend.description].filter(Boolean).join(" · ")}
                          </span>
                        ) : null}
                      </td>
                      <td class="mono">{backend.driver === "lvm-thin" ? "LVM thin" : backend.driver}</td>
                      <td class="mono">
                        {backend.config?.vg || "—"}/{backend.config?.thin_pool || "—"}
                        {backend.config?.supports_templates ? (
                          <span class="import-disk-badge template-backend-badge">templates</span>
                        ) : null}
                      </td>
                      <td class={backend.disk_count ? "mono" : "mono muted"}>{String(backend.disk_count || 0)}</td>
                      <td class="updated hide-sm">{formatDate(backend.updated_at)}</td>
                      <td>
                        <div class="actions">
                          <button
                            class="row-action"
                            type="button"
                            disabled={!importAvailable}
                            title={
                              importAvailable
                                ? "Import existing thin volumes from this backend."
                                : "Import is available for configured LVM thin backends."
                            }
                            onClick={() => navigate(`/storage-backends/${backend.id}/import`)}
                          >
                            Import
                          </button>
                          <button
                            class="row-action"
                            type="button"
                            onClick={() => navigate(`/storage-backends/${backend.id}/edit`)}
                          >
                            Edit
                          </button>
                          <button
                            class="row-action danger"
                            type="button"
                            disabled={backend.disk_count > 0}
                            title={backend.disk_count > 0 ? "Delete disks using this backend first." : ""}
                            onClick={() => void deleteBackend(backend)}
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
                ▤
              </div>
              <h3>No storage backends yet</h3>
              <p>Register an LVM thin pool before creating disks.</p>
              <button class="button primary" type="button" onClick={() => navigate("/storage-backends/new")}>
                Add a storage backend
              </button>
            </div>
          )}
        </div>
      </section>

      <section class="panel panel-spaced" aria-labelledby="disks-heading">
        <div class="panel-head">
          <div>
            <h2 id="disks-heading">Disks</h2>
            <p class="panel-subtitle">{diskSummary}</p>
          </div>
          <button class="button ghost" type="button" disabled={loading} onClick={() => void refreshAll()}>
            {loading ? "Refreshing…" : "Refresh"}
          </button>
        </div>

        {diskError ? (
          <div class="banner" role="alert">
            {diskError}
          </div>
        ) : null}

        <div class="table-wrap">
          {disks.length > 0 ? (
            <table>
              <thead>
                <tr>
                  <th scope="col">Name</th>
                  <th scope="col">Size</th>
                  <th scope="col">Backend</th>
                  <th scope="col">Attached to</th>
                  <th scope="col" class="hide-sm">
                    Updated
                  </th>
                  <th scope="col">
                    <span class="sr-only">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {disks.map((disk) => (
                  <tr key={disk.id}>
                    <td>
                      <span class="vm-name">{disk.name}</span>
                      {disk.is_template ? <span class="import-disk-badge">template</span> : null}
                      {disk.template_disk_name ? (
                        <span class="vm-description">from {disk.template_disk_name}</span>
                      ) : null}
                      {disk.device ? (
                        <span class="vm-description" title={disk.device}>
                          {disk.device}
                        </span>
                      ) : null}
                    </td>
                    <td class="mono">{numberFormat.format(disk.size_gb)} GB</td>
                    <td class={disk.storage_backend_name ? "mono" : "mono muted"}>
                      {disk.storage_backend_name || "—"}
                    </td>
                    <td class={disk.is_template ? "mono muted" : disk.attached_vm_id ? "mono" : "mono muted"}>
                      {disk.is_template
                        ? "template"
                        : disk.attached_vm_id
                          ? disk.attached_vm_name || disk.attached_vm_id
                          : "unattached"}
                    </td>
                    <td class="updated hide-sm">{formatDate(disk.updated_at)}</td>
                    <td>
                      <div class="actions">
                        {disk.is_template ? (
                          <button class="row-action" type="button" onClick={() => setProvisioningDisk(disk)}>
                            Provisioning
                          </button>
                        ) : null}
                        <button
                          class="row-action"
                          type="button"
                          disabled={Boolean(disk.attached_vm_id)}
                          title={disk.is_template ? "Allow this disk to be used as a normal disk again." : "Mark this unattached disk as a template."}
                          onClick={() => void toggleTemplate(disk)}
                        >
                          {disk.is_template ? "Unmark" : "Template"}
                        </button>
                        <button
                          class="row-action danger"
                          type="button"
                          disabled={Boolean(disk.attached_vm_id)}
                          title={
                            disk.attached_vm_id
                              ? `Detach ${disk.name} from ${disk.attached_vm_name || "its VM"} before deleting it.`
                              : ""
                          }
                          onClick={() => void deleteDisk(disk)}
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
                ▣
              </div>
              <h3>No disks yet</h3>
              <p>
                Create a disk here, or use <strong>Import</strong> on a storage backend to register
                existing thin volumes.
              </p>
              <button
                class="button primary"
                type="button"
                disabled={backends.length === 0}
                title={backends.length === 0 ? "Add a storage backend before creating disks" : ""}
                onClick={() => navigate("/disks/new")}
              >
                Create a disk
              </button>
            </div>
          )}
        </div>
      </section>

      {provisioningDisk ? (
        <TemplateProvisioningDialog
          disk={provisioningDisk}
          onClose={() => setProvisioningDisk(null)}
          onSaved={(disk) => {
            setProvisioningDisk(null);
            publishDisks(disks.map((item) => (item.id === disk.id ? disk : item)));
            showToast(`Saved provisioning for ${disk.name}`);
          }}
        />
      ) : null}

      {diskDialogOpen ? (
        <DiskDialog
          backends={backends}
          disks={disks}
          onClose={() => {
            setDiskDialogOpen(false);
            navigate("/disks", true);
          }}
          onSaved={(disk) => {
            setDiskDialogOpen(false);
            navigate("/disks", true);
            setDiskError("");
            showToast(`Created disk ${disk.name}`);
            void loadDisks();
            void loadBackends(false);
          }}
        />
      ) : null}

      {backendDialog ? (
        <StorageBackendDialog
          key={backendDialog.backend ? `edit-${backendDialog.backend.id}` : "new"}
          backend={backendDialog.backend}
          onClose={() => {
            setBackendDialog(null);
            navigate("/disks", true);
          }}
          onSaved={(saved) => {
            setBackendDialog(null);
            navigate("/disks", true);
            setBackendError("");
            showToast(`${backendDialog.backend ? "Saved" : "Added"} storage backend ${saved.name}`);
            void loadBackends();
            void loadDisks(false);
          }}
        />
      ) : null}

      {importBackend ? (
        <ImportDisksDialog
          key={`import-${importBackend.id}`}
          backend={importBackend}
          onClose={() => {
            setImportBackend(null);
            navigate("/disks", true);
          }}
          onImported={(imported) => {
            setImportBackend(null);
            navigate("/disks", true);
            setDiskError("");
            showToast(`Imported ${imported.length} disk${imported.length === 1 ? "" : "s"}`);
            void loadDisks();
            void loadBackends(false);
          }}
        />
      ) : null}
    </>
  );
}
