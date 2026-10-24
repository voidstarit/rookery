import { useEffect, useRef, useState } from "preact/hooks";
import { api } from "../../shared/api";
import type { VirtualInterface, VM } from "../types";

type AttachDialogProps = {
  iface: VirtualInterface;
  vms: VM[];
  onClose: () => void;
  onSaved: (iface: VirtualInterface) => void;
};

export function AttachDialog({ iface, vms, onClose, onSaved }: AttachDialogProps) {
  const [vmID, setVMID] = useState(iface.attached_vm_id || "");
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
    setError("");
    setSaving(true);
    try {
      const updated = await api<VirtualInterface>(`/api/interfaces/${iface.id}`, {
        method: "PUT",
        body: JSON.stringify({ attached_vm_id: vmID || null }),
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
      id="attach-dialog"
      aria-labelledby="attach-dialog-title"
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
            <p class="eyebrow">VM attachment</p>
            <h2 id="attach-dialog-title">Attach interface</h2>
          </div>
          <button class="icon-button" type="button" aria-label="Close dialog" onClick={close}>
            ×
          </button>
        </header>

        <div class="form-grid">
          <label class="field field-wide">
            <span class="field-label">Interface</span>
            <input type="text" readonly value={iface.name} />
          </label>

          <label class="field field-wide">
            <span class="field-label">Attach to VM</span>
            <select value={vmID} onChange={(event) => setVMID(event.currentTarget.value)}>
              <option value="">Leave unattached</option>
              {vms.map((vm) => {
                const running = vm.status === "running" || vm.status === "starting";
                return (
                  <option value={vm.id} key={vm.id}>
                    {running ? `${vm.name} (running — stop first)` : vm.name}
                  </option>
                );
              })}
            </select>
            <span class="field-hint">Interfaces take effect when the VM is started.</span>
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
          <button class="button primary" type="submit" disabled={saving}>
            {saving ? "Saving…" : "Save attachment"}
          </button>
        </footer>
      </form>
    </dialog>
  );
}
