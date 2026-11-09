import { useEffect, useRef, useState } from "preact/hooks";
import { api } from "../../shared/api";
import type { DiskThrottleClass } from "../types";
import {
  draftFromLimits,
  limitsFromDraft,
  validateDraft,
  type ThrottleDraft,
  type ThrottleMode,
} from "./throttle";

type DiskThrottleClassDialogProps = {
  classItem: DiskThrottleClass | null;
  onClose: () => void;
  onSaved: (item: DiskThrottleClass) => void;
};

const NAME_PATTERN = "[A-Za-z0-9][A-Za-z0-9._\\-]*";

export function DiskThrottleClassDialog({ classItem, onClose, onSaved }: DiskThrottleClassDialogProps) {
  const dialogRef = useRef<HTMLDialogElement | null>(null);
  const nameRef = useRef<HTMLInputElement | null>(null);
  const [name, setName] = useState(classItem?.name || "");
  const [description, setDescription] = useState(classItem?.description || "");
  const [draft, setDraft] = useState<ThrottleDraft>(() => draftFromLimits(classItem?.limits));
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    dialogRef.current?.showModal();
    nameRef.current?.focus();
  }, []);

  const validation = validateDraft(draft);

  const close = () => {
    if (!saving) {
      dialogRef.current?.close();
    }
  };

  const update = (key: keyof ThrottleDraft, value: string) => {
    setDraft((current) => ({ ...current, [key]: value }));
  };

  const submit = async (event: Event) => {
    event.preventDefault();
    if (saving) {
      return;
    }
    if (!name.trim()) {
      setError("Name is required.");
      return;
    }
    if (validation) {
      setError(validation);
      return;
    }
    setError("");
    setSaving(true);
    try {
      const body = {
        name: name.trim(),
        description: description.trim(),
        limits: limitsFromDraft(draft),
      };
      const saved = classItem
        ? await api<DiskThrottleClass>(`/api/disk-throttle-classes/${classItem.id}`, {
            method: "PUT",
            body: JSON.stringify(body),
          })
        : await api<DiskThrottleClass>("/api/disk-throttle-classes", {
            method: "POST",
            body: JSON.stringify(body),
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
      id="disk-throttle-class-dialog"
      aria-labelledby="disk-throttle-class-dialog-title"
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
            <p class="eyebrow">{classItem ? "Edit policy" : "New policy"}</p>
            <h2 id="disk-throttle-class-dialog-title">
              {classItem ? `Edit ${classItem.name}` : "Create throttling class"}
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
              placeholder="web-standard"
              value={name}
              onInput={(event) => setName(event.currentTarget.value)}
            />
            <span class="field-hint">Letters, digits, dots, dashes and underscores.</span>
          </label>
          <label class="field field-wide">
            <span class="field-label">Description</span>
            <input
              type="text"
              maxlength={2000}
              autocomplete="off"
              placeholder="Optional"
              value={description}
              onInput={(event) => setDescription(event.currentTarget.value)}
            />
          </label>

          <div class="field field-wide throttle-group">
            <span class="field-label">Bandwidth</span>
            <select
              value={draft.bpsMode}
              onChange={(event) => update("bpsMode", event.currentTarget.value as ThrottleMode)}
            >
              <option value="none">Unlimited</option>
              <option value="total">Total</option>
              <option value="directional">Read/Write</option>
            </select>
            {draft.bpsMode === "total" ? (
              <div class="throttle-fields">
                <LimitInput
                  label="Total BPS"
                  value={draft.bpsTotal}
                  onChange={(value) => update("bpsTotal", value)}
                />
                <LimitInput
                  label="Burst maximum (BPS)"
                  value={draft.bpsTotalMax}
                  onChange={(value) => update("bpsTotalMax", value)}
                />
                <LimitInput
                  label="Burst length (seconds)"
                  value={draft.bpsTotalMaxLength}
                  onChange={(value) => update("bpsTotalMaxLength", value)}
                />
              </div>
            ) : null}
            {draft.bpsMode === "directional" ? (
              <div class="throttle-fields">
                <LimitInput label="Read BPS" value={draft.bpsRead} onChange={(value) => update("bpsRead", value)} />
                <LimitInput
                  label="Read burst maximum (BPS)"
                  value={draft.bpsReadMax}
                  onChange={(value) => update("bpsReadMax", value)}
                />
                <LimitInput
                  label="Read burst length (seconds)"
                  value={draft.bpsReadMaxLength}
                  onChange={(value) => update("bpsReadMaxLength", value)}
                />
                <LimitInput label="Write BPS" value={draft.bpsWrite} onChange={(value) => update("bpsWrite", value)} />
                <LimitInput
                  label="Write burst maximum (BPS)"
                  value={draft.bpsWriteMax}
                  onChange={(value) => update("bpsWriteMax", value)}
                />
                <LimitInput
                  label="Write burst length (seconds)"
                  value={draft.bpsWriteMaxLength}
                  onChange={(value) => update("bpsWriteMaxLength", value)}
                />
              </div>
            ) : null}
            <span class="field-hint">Bytes per second. Burst lengths are seconds.</span>
          </div>

          <div class="field field-wide throttle-group">
            <span class="field-label">IOPS</span>
            <select
              value={draft.iopsMode}
              onChange={(event) => update("iopsMode", event.currentTarget.value as ThrottleMode)}
            >
              <option value="none">Unlimited</option>
              <option value="total">Total</option>
              <option value="directional">Read/Write</option>
            </select>
            {draft.iopsMode === "total" ? (
              <div class="throttle-fields">
                <LimitInput
                  label="Total IOPS"
                  value={draft.iopsTotal}
                  onChange={(value) => update("iopsTotal", value)}
                />
                <LimitInput
                  label="Burst maximum (IOPS)"
                  value={draft.iopsTotalMax}
                  onChange={(value) => update("iopsTotalMax", value)}
                />
                <LimitInput
                  label="Burst length (seconds)"
                  value={draft.iopsTotalMaxLength}
                  onChange={(value) => update("iopsTotalMaxLength", value)}
                />
              </div>
            ) : null}
            {draft.iopsMode === "directional" ? (
              <div class="throttle-fields">
                <LimitInput label="Read IOPS" value={draft.iopsRead} onChange={(value) => update("iopsRead", value)} />
                <LimitInput
                  label="Read burst maximum (IOPS)"
                  value={draft.iopsReadMax}
                  onChange={(value) => update("iopsReadMax", value)}
                />
                <LimitInput
                  label="Read burst length (seconds)"
                  value={draft.iopsReadMaxLength}
                  onChange={(value) => update("iopsReadMaxLength", value)}
                />
                <LimitInput label="Write IOPS" value={draft.iopsWrite} onChange={(value) => update("iopsWrite", value)} />
                <LimitInput
                  label="Write burst maximum (IOPS)"
                  value={draft.iopsWriteMax}
                  onChange={(value) => update("iopsWriteMax", value)}
                />
                <LimitInput
                  label="Write burst length (seconds)"
                  value={draft.iopsWriteMaxLength}
                  onChange={(value) => update("iopsWriteMaxLength", value)}
                />
              </div>
            ) : null}
            <span class="field-hint">I/O operations per second. Burst lengths are seconds.</span>
          </div>

          {draft.iopsMode !== "none" ? (
            <LimitInput
              label="I/O size (bytes)"
              value={draft.iopsSize}
              onChange={(value) => update("iopsSize", value)}
            />
          ) : null}

          {validation && !error ? (
            <p class="field-hint field-wide">{validation}</p>
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
          <button class="button primary" type="submit" disabled={saving || Boolean(validation)}>
            {saving ? "Saving…" : classItem ? "Save class" : "Create class"}
          </button>
        </footer>
      </form>
    </dialog>
  );
}

function LimitInput({
  label,
  value,
  onChange,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
}) {
  return (
    <label class="field">
      <span class="field-label">{label}</span>
      <input
        type="number"
        min={0}
        step={1}
        placeholder="0"
        value={value}
        onInput={(event) => onChange(event.currentTarget.value)}
      />
    </label>
  );
}
