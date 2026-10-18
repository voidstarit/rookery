import { useEffect, useRef, useState } from "preact/hooks";
import { api } from "../../shared/api";
import type { Disk, StorageBackend } from "../types";

type DiskDialogProps = {
  backends: StorageBackend[];
  onClose: () => void;
  onSaved: (disk: Disk) => void;
};

const NAME_PATTERN = "[A-Za-z0-9][A-Za-z0-9._-]*";

export function DiskDialog({ backends, onClose, onSaved }: DiskDialogProps) {
  const defaultBackend = backends.find((item) => item.name === "default") || backends[0];
  const [name, setName] = useState("");
  const [sizeGB, setSizeGB] = useState("10");
  const [backendID, setBackendID] = useState(defaultBackend ? String(defaultBackend.id) : "");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const dialogRef = useRef<HTMLDialogElement | null>(null);
  const nameRef = useRef<HTMLInputElement | null>(null);
  const formRef = useRef<HTMLFormElement | null>(null);

  useEffect(() => {
    dialogRef.current?.showModal();
    nameRef.current?.focus();
  }, []);

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
    setError("");
    setSaving(true);
    try {
      const disk = await api<Disk>("/api/disks", {
        method: "POST",
        body: JSON.stringify({
          name: name.trim(),
          size_gb: Number(sizeGB),
          storage_backend_id: Number(backendID),
        }),
      });
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
            <span class="field-label">Size (GB)</span>
            <input
              type="number"
              required
              min={1}
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
              onChange={(event) => setBackendID(event.currentTarget.value)}
            >
              {backends.map((backend) => (
                <option value={String(backend.id)} key={backend.id}>
                  {backend.name} · {backend.driver === "lvm-thin" ? "LVM thin" : backend.driver}
                </option>
              ))}
            </select>
            <span class="field-hint">The thin pool or storage driver that will hold this disk.</span>
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
          <button class="button primary" type="submit" disabled={saving || backends.length === 0}>
            {saving ? "Creating…" : "Create disk"}
          </button>
        </footer>
      </form>
    </dialog>
  );
}
