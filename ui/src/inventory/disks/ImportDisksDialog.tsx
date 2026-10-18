import { useEffect, useRef, useState } from "preact/hooks";
import { api } from "../../shared/api";
import type { Disk, ImportableDisk, StorageBackend } from "../types";

type ImportDisksDialogProps = {
  backend: StorageBackend;
  onClose: () => void;
  onImported: (disks: Disk[]) => void;
};

export function ImportDisksDialog({ backend, onClose, onImported }: ImportDisksDialogProps) {
  const [items, setItems] = useState<ImportableDisk[]>([]);
  const [selected, setSelected] = useState<Set<string>>(() => new Set());
  const [loading, setLoading] = useState(true);
  const [importing, setImporting] = useState(false);
  const [error, setError] = useState("");
  const dialogRef = useRef<HTMLDialogElement | null>(null);

  const load = async () => {
    setLoading(true);
    setError("");
    try {
      const data = await api<ImportableDisk[]>(`/api/storage-backends/${backend.id}/importable-disks`);
      setItems(Array.isArray(data) ? data : []);
      setSelected(new Set());
    } catch (err) {
      setItems([]);
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    dialogRef.current?.showModal();
    void load();
    // The dialog is remounted for each backend.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const close = () => {
    if (!importing) {
      dialogRef.current?.close();
    }
  };

  const toggle = (name: string, checked: boolean) => {
    setSelected((current) => {
      const next = new Set(current);
      if (checked) {
        next.add(name);
      } else {
        next.delete(name);
      }
      return next;
    });
  };

  const importSelected = async () => {
    if (importing || selected.size === 0) {
      return;
    }
    setImporting(true);
    setError("");
    try {
      const imported = await api<Disk[]>(`/api/storage-backends/${backend.id}/import-disks`, {
        method: "POST",
        body: JSON.stringify({ names: [...selected] }),
      });
      onImported(Array.isArray(imported) ? imported : []);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setImporting(false);
    }
  };

  return (
    <dialog
      ref={dialogRef}
      id="import-disk-dialog"
      aria-labelledby="import-disk-dialog-title"
      onClose={onClose}
      onClick={(event) => {
        if (event.target === event.currentTarget) {
          close();
        }
      }}
      onCancel={(event) => {
        if (importing) {
          event.preventDefault();
        }
      }}
    >
      <header class="dialog-head">
        <div>
          <p class="eyebrow">Storage backend</p>
          <h2 id="import-disk-dialog-title">Import existing disks</h2>
        </div>
        <button class="icon-button" type="button" aria-label="Close dialog" onClick={close}>
          ×
        </button>
      </header>

      <p class="import-disk-lede">
        {backend.name} · {backend.config.vg}/{backend.config.thin_pool} — select existing thin
        volumes to register as disks. Importing does not modify LVM, but deleting an imported disk
        later removes its volume.
      </p>

      {error ? (
        <div class="banner" role="alert">
          {error}
        </div>
      ) : null}

      <div class="import-disk-list">
        {loading ? <p class="import-disk-meta">Loading existing thin volumes…</p> : null}
        {!loading && items.length === 0 && !error ? (
          <div class="empty import-disk-empty">
            <div class="empty-mark" aria-hidden="true">
              ▣
            </div>
            <h3>No thin volumes found</h3>
            <p>This storage backend has no existing thin volumes to import.</p>
          </div>
        ) : null}
        {!loading
          ? items.map((item) => (
              <label
                class={item.registered ? "import-disk-row imported" : "import-disk-row"}
                key={item.name}
              >
                <input
                  type="checkbox"
                  value={item.name}
                  checked={selected.has(item.name)}
                  disabled={item.registered || importing}
                  aria-label={`Import ${item.name}`}
                  onChange={(event) => toggle(item.name, event.currentTarget.checked)}
                />
                <span class="import-disk-copy">
                  <span class="import-disk-name">{item.name}</span>
                  <span class="import-disk-meta">
                    {item.size_gb} GB ·{" "}
                    {item.registered
                      ? item.attached_vm_name
                        ? `already imported · attached to ${item.attached_vm_name}`
                        : item.storage_backend_name
                          ? `already imported · ${item.storage_backend_name}`
                          : "already imported"
                      : "available to import"}
                  </span>
                </span>
                <span class="import-disk-badge">{item.registered ? "imported" : "new"}</span>
              </label>
            ))
          : null}
      </div>

      <footer class="dialog-actions">
        <button class="button ghost" type="button" disabled={loading || importing} onClick={() => void load()}>
          {loading ? "Refreshing…" : "Refresh"}
        </button>
        <button class="button ghost" type="button" disabled={importing} onClick={close}>
          Cancel
        </button>
        <button
          class="button primary"
          type="button"
          disabled={importing || selected.size === 0}
          onClick={() => void importSelected()}
        >
          {importing ? "Importing…" : selected.size > 0 ? `Import selected (${selected.size})` : "Import selected"}
        </button>
      </footer>
    </dialog>
  );
}
