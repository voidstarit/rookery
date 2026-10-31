import { useEffect, useRef, useState } from "preact/hooks";
import { api } from "../../shared/api";
import {
  LabelsInput,
  saveEntityLabelsWithToast,
  useEntityLabelDraft,
} from "../../shared/labels";
import type { LightVMInitramfs, LightVMKernel } from "../types";
import { removeUploadedLightVMFile, uploadLightVMFile } from "./upload";

export type LightVMArtifactKind = "kernel" | "initramfs";
export type LightVMArtifact = LightVMKernel | LightVMInitramfs;

type ArtifactConfig = {
  endpoint: string;
  entityType: string;
  uploadKind: "kernel" | "initramfs";
  noun: string;
  namePlaceholder: string;
};

const ARTIFACTS: Record<LightVMArtifactKind, ArtifactConfig> = {
  kernel: {
    endpoint: "/api/lightvm/kernels",
    entityType: "lightvm_kernel",
    uploadKind: "kernel",
    noun: "kernel",
    namePlaceholder: "vmlinuz-6.12",
  },
  initramfs: {
    endpoint: "/api/lightvm/initramfs",
    entityType: "lightvm_initramfs",
    uploadKind: "initramfs",
    noun: "initramfs",
    namePlaceholder: "initramfs-6.12",
  },
};

type ArtifactDialogProps = {
  kind: LightVMArtifactKind;
  artifact: LightVMArtifact | null;
  onClose: () => void;
  onSaved: (artifact: LightVMArtifact, created: boolean) => void;
};

const NAME_PATTERN = "[A-Za-z0-9][A-Za-z0-9._\\-]*";

function formatBytes(bytes: number): string {
  const value = Number(bytes) || 0;
  if (value >= 1024 ** 3) {
    return `${(value / 1024 ** 3).toFixed(1)} GB`;
  }
  if (value >= 1024 ** 2) {
    return `${(value / 1024 ** 2).toFixed(1)} MB`;
  }
  if (value >= 1024) {
    return `${(value / 1024).toFixed(1)} KB`;
  }
  return `${value} B`;
}

export function ArtifactDialog({ kind, artifact, onClose, onSaved }: ArtifactDialogProps) {
  const config = ARTIFACTS[kind];
  const editing = artifact !== null;
  const [name, setName] = useState(artifact?.name || "");
  const [description, setDescription] = useState(artifact?.description || "");
  const [file, setFile] = useState<File | null>(null);
  const [labels, setLabels] = useEntityLabelDraft(config.entityType, artifact?.id);
  const [saving, setSaving] = useState(false);
  const [progress, setProgress] = useState<number | null>(null);
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
    if (!editing && !file) {
      setError(`Choose an ${config.noun} file to upload.`);
      return;
    }

    setError("");
    setSaving(true);
    let uploadedPath = "";
    try {
      let path = artifact?.path || "";
      let fileName = artifact?.file_name || "";
      let sizeBytes = artifact?.size_bytes || 0;
      if (file) {
        setProgress(0);
        const uploaded = await uploadLightVMFile(config.uploadKind, file, setProgress);
        uploadedPath = uploaded.path;
        path = uploaded.path;
        fileName = uploaded.file_name;
        sizeBytes = uploaded.size_bytes;
      }

      const payload = {
        name: name.trim(),
        description: description.trim(),
        path,
        file_name: fileName,
      };
      const saved = editing
        ? await api<LightVMArtifact>(`${config.endpoint}/${artifact.id}`, {
            method: "PUT",
            body: JSON.stringify(payload),
          })
        : await api<LightVMArtifact>(config.endpoint, {
            method: "POST",
            body: JSON.stringify(payload),
          });
      uploadedPath = "";
      await saveEntityLabelsWithToast(config.entityType, saved.id, saved.name, labels);
      onSaved(
        {
          ...saved,
          size_bytes: saved.size_bytes || sizeBytes,
        },
        !editing
      );
    } catch (err) {
      if (uploadedPath) {
        await removeUploadedLightVMFile(uploadedPath).catch(() => undefined);
      }
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
      setProgress(null);
    }
  };

  return (
    <dialog
      ref={dialogRef}
      id={`${config.uploadKind}-dialog`}
      aria-labelledby={`${config.uploadKind}-dialog-title`}
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
            <p class="eyebrow">
              {editing ? `Edit ${config.noun}` : `New ${config.noun}`}
            </p>
            <h2 id={`${config.uploadKind}-dialog-title`}>
              {editing ? `Edit LightVM ${config.noun}` : `Add LightVM ${config.noun}`}
            </h2>
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
              placeholder={config.namePlaceholder}
              value={name}
              onInput={(event) => setName(event.currentTarget.value)}
            />
            <span class="field-hint">A short label for this {config.noun} image.</span>
          </label>

          <label class="field field-wide">
            <span class="field-label">Description</span>
            <input
              type="text"
              maxlength={2000}
              autocomplete="off"
              placeholder="Optional notes, version, or source"
              value={description}
              onInput={(event) => setDescription(event.currentTarget.value)}
            />
          </label>

          <label class="field field-wide">
            <span class="field-label">
              {editing ? `Replace ${config.noun} file` : `${config.noun[0].toUpperCase()}${config.noun.slice(1)} file`}
            </span>
            <input
              type="file"
              required={!editing}
              disabled={saving}
              onChange={(event) => setFile(event.currentTarget.files?.[0] || null)}
            />
            <span class="field-hint">
              {editing && artifact
                ? `Current file: ${artifact.file_name || artifact.path} (${formatBytes(artifact.size_bytes)}). Choose a file only to replace it.`
                : "The file is uploaded to the Rookery host and stored under the configured LightVM directory."}
            </span>
            {file ? (
              <span class="field-hint">
                Selected: {file.name} ({formatBytes(file.size)})
              </span>
            ) : null}
          </label>

          {progress !== null ? (
            <div class="field field-wide">
              <span class="field-label">Uploading</span>
              <progress max={100} value={Math.round(progress * 100)} />
              <span class="field-hint">{Math.round(progress * 100)}%</span>
            </div>
          ) : null}

          <div class="field field-wide">
            <LabelsInput labels={labels} onChange={setLabels} disabled={saving} />
          </div>
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
            {saving
              ? progress !== null
                ? "Uploading…"
                : "Saving…"
              : editing
                ? `Save ${config.noun}`
                : `Add ${config.noun}`}
          </button>
        </footer>
      </form>
    </dialog>
  );
}
