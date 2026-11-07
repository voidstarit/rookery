import { useEffect, useRef, useState } from "preact/hooks";
import { api } from "../../shared/api";
import type { Disk } from "../types";

type DiskBandwidthDialogProps = {
  disk: Disk;
  onClose: () => void;
  onSaved: (disk: Disk) => void;
};

export function DiskBandwidthDialog({ disk, onClose, onSaved }: DiskBandwidthDialogProps) {
  const [readBPS, setReadBPS] = useState(String(disk.read_bps ?? 0));
  const [writeBPS, setWriteBPS] = useState(String(disk.write_bps ?? 0));
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const dialogRef = useRef<HTMLDialogElement | null>(null);

  useEffect(() => {
    dialogRef.current?.showModal();
  }, []);

  const close = () => {
    if (!saving) {
      dialogRef.current?.close();
    }
  };

  const submit = async (event: Event) => {
    event.preventDefault();
    if (saving) {
      return;
    }
    const read = Number(readBPS) || 0;
    const write = Number(writeBPS) || 0;
    if (read < 0 || write < 0) {
      setError("Bandwidth limits must be zero or greater.");
      return;
    }
    setSaving(true);
    setError("");
    try {
      const saved = await api<Disk>(`/api/disks/${disk.id}`, {
        method: "PUT",
        body: JSON.stringify({ read_bps: read, write_bps: write }),
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
      class="dialog"
      aria-labelledby="disk-bandwidth-title"
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
      <form novalidate onSubmit={(event) => void submit(event)}>
        <header class="dialog-head">
          <div>
            <p class="eyebrow">Disk limits</p>
            <h2 id="disk-bandwidth-title">{disk.name}</h2>
          </div>
          <button class="icon-button" type="button" aria-label="Close dialog" onClick={close}>
            ×
          </button>
        </header>
        <div class="form-grid">
          <label class="field">
            <span class="field-label">Read bandwidth limit (bytes/s)</span>
            <input
              type="number"
              min={0}
              step={1}
              value={readBPS}
              onInput={(event) => setReadBPS(event.currentTarget.value)}
            />
          </label>
          <label class="field">
            <span class="field-label">Write bandwidth limit (bytes/s)</span>
            <input
              type="number"
              min={0}
              step={1}
              value={writeBPS}
              onInput={(event) => setWriteBPS(event.currentTarget.value)}
            />
          </label>
          <p class="field-hint field-wide">
            0 means unlimited. QEMU applies these limits when the disk is attached to a VM start;
            a running VM keeps its current limits until it is restarted.
          </p>
          {error ? (
            <p class="form-error field-wide" role="alert">
              {error}
            </p>
          ) : null}
        </div>
        <div class="dialog-actions">
          <button class="button ghost" type="button" disabled={saving} onClick={close}>
            Cancel
          </button>
          <button class="button primary" type="submit" disabled={saving}>
            {saving ? "Saving…" : "Save limits"}
          </button>
        </div>
      </form>
    </dialog>
  );
}
