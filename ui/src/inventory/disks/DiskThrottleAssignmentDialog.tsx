import { useEffect, useRef, useState } from "preact/hooks";
import { api } from "../../shared/api";
import type { Disk, DiskThrottleClass } from "../types";
import { formatThrottleSummary } from "./throttle";

type DiskThrottleAssignmentDialogProps = {
  disk: Disk;
  classes: DiskThrottleClass[];
  onClose: () => void;
  onSaved: (disk: Disk) => void;
};

export function DiskThrottleAssignmentDialog({
  disk,
  classes,
  onClose,
  onSaved,
}: DiskThrottleAssignmentDialogProps) {
  const dialogRef = useRef<HTMLDialogElement | null>(null);
  const [selected, setSelected] = useState(String(disk.throttle_class_id ?? 0));
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    dialogRef.current?.showModal();
  }, []);

  const availableClasses = classes.filter((item) => !item.validation_error);
  const selectedClass = availableClasses.find((item) => String(item.id) === selected) || null;
  const disabled = disk.is_template;

  const close = () => {
    if (!saving) {
      dialogRef.current?.close();
    }
  };

  const submit = async (event: Event) => {
    event.preventDefault();
    if (saving || disabled) {
      return;
    }
    setError("");
    setSaving(true);
    try {
      const updated = await api<Disk>(`/api/disks/${disk.id}`, {
        method: "PUT",
        body: JSON.stringify({ throttle_class_id: Number(selected) || 0 }),
      });
      onSaved(updated);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  };

  return (
    <dialog
      ref={dialogRef}
      id="disk-throttle-assignment-dialog"
      aria-labelledby="disk-throttle-assignment-title"
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
      <form onSubmit={(event) => void submit(event)}>
        <header class="dialog-head">
          <div>
            <p class="eyebrow">Disk throttling</p>
            <h2 id="disk-throttle-assignment-title">{disk.name}</h2>
          </div>
          <button class="icon-button" type="button" aria-label="Close dialog" onClick={close}>
            ×
          </button>
        </header>

        <div class="form-grid">
          <label class="field field-wide">
            <span class="field-label">Throttling class</span>
            <select
              value={selected}
              disabled={saving || disabled}
              onChange={(event) => setSelected(event.currentTarget.value)}
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
              Configured limits apply when the VM next starts. A running VM keeps its current
              QEMU throttling arguments until it restarts.
            </span>
          </label>
          {selectedClass ? (
            <div class="field field-wide">
              <span class="field-label">Selected policy</span>
              <p class="field-hint">
                {formatThrottleSummary(selectedClass.limits) || "No limits configured."} · Assigned
                to {selectedClass.disk_count ?? 0} disk(s).
              </p>
            </div>
          ) : null}
          {disabled ? (
            <p class="field-hint field-wide">Templates cannot carry a throttling class.</p>
          ) : null}
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
          <button class="button primary" type="submit" disabled={saving || disabled}>
            {saving ? "Saving…" : "Save"}
          </button>
        </footer>
      </form>
    </dialog>
  );
}
