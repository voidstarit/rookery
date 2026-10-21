import { useEffect, useRef, useState } from "preact/hooks";
import { api } from "../../shared/api";
import type { Media } from "../types";

type MediaDialogProps = {
  onClose: () => void;
  onSaved: (media: Media) => void;
};

const NAME_PATTERN = "[A-Za-z0-9][A-Za-z0-9._\\-]*";

export function MediaDialog({ onClose, onSaved }: MediaDialogProps) {
  const [name, setName] = useState("");
  const [path, setPath] = useState("");
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
      const media = await api<Media>("/api/media", {
        method: "POST",
        body: JSON.stringify({ name: name.trim(), path: path.trim() }),
      });
      onSaved(media);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  };

  return (
    <dialog
      ref={dialogRef}
      id="media-dialog"
      aria-labelledby="media-dialog-title"
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
            <p class="eyebrow">New media</p>
            <h2 id="media-dialog-title">Add installation media</h2>
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
              placeholder="debian-13-netinst"
              value={name}
              onInput={(event) => setName(event.currentTarget.value)}
            />
            <span class="field-hint">A short label for the media.</span>
          </label>

          <label class="field field-wide">
            <span class="field-label">ISO path</span>
            <input
              type="text"
              required
              autocomplete="off"
              spellcheck={false}
              placeholder="/srv/isos/debian-13.1.0-amd64-netinst.iso"
              value={path}
              onInput={(event) => setPath(event.currentTarget.value)}
            />
            <span class="field-hint">
              Absolute path to an existing read-only file on the host. The file is never modified.
            </span>
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
            {saving ? "Adding…" : "Add media"}
          </button>
        </footer>
      </form>
    </dialog>
  );
}
