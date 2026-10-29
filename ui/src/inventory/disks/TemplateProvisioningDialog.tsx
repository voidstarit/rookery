import { useEffect, useRef, useState } from "preact/hooks";
import { api } from "../../shared/api";
import type { Disk } from "../types";

type TemplateProvisioningDialogProps = {
  disk: Disk;
  onClose: () => void;
  onSaved: (disk: Disk) => void;
};

export function TemplateProvisioningDialog({ disk, onClose, onSaved }: TemplateProvisioningDialogProps) {
  const [script, setScript] = useState(disk.provisioning_script || "");
  const [timeout, setTimeoutValue] = useState(disk.provisioning_timeout || "");
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
    setSaving(true);
    setError("");
    try {
      const updated = await api<Disk>(`/api/disks/${disk.id}`, {
        method: "PUT",
        body: JSON.stringify({
          provisioning_script: script.trim(),
          provisioning_timeout: timeout.trim(),
        }),
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
      id="template-provisioning-dialog"
      aria-labelledby="template-provisioning-title"
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
            <p class="eyebrow">Template provisioning</p>
            <h2 id="template-provisioning-title">Provisioning · {disk.name}</h2>
          </div>
          <button class="icon-button" type="button" aria-label="Close dialog" onClick={close}>
            ×
          </button>
        </header>

        <label class="field field-wide">
          <span class="field-label">Provisioning script</span>
          <input
            type="text"
            placeholder="/usr/local/sbin/rookery-provision-template"
            value={script}
            onInput={(event) => setScript(event.currentTarget.value)}
          />
          <span class="field-hint">
            Absolute path to a script run after the backend clones this template. It receives
            the provisioning context and custom properties in <code>ROOKERY_PROVISION_JSON</code>.
            Leave empty to disable provisioning.
          </span>
        </label>

        <label class="field">
          <span class="field-label">Timeout</span>
          <input
            type="text"
            placeholder="5m"
            value={timeout}
            onInput={(event) => setTimeoutValue(event.currentTarget.value)}
          />
          <span class="field-hint">Examples: 30s, 5m, 1h.</span>
        </label>

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
            {saving ? "Saving…" : "Save provisioning"}
          </button>
        </footer>
      </form>
    </dialog>
  );
}
