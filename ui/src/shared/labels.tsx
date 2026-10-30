import { useEffect, useRef, useState } from "preact/hooks";
import { api } from "./api";
import { showToast } from "./toast";

export type EntityLabelsRecord = {
  entity_type: string;
  entity_id: string;
  labels: string[];
};

export type EntityLabelMap = Record<string, string[]>;

const MAX_LABELS = 64;

export function useEntityLabels(entityType: string) {
  const [labels, setLabelsState] = useState<EntityLabelMap>({});

  const reload = async () => {
    try {
      const records = await api<EntityLabelsRecord[]>(
        `/api/labels/${encodeURIComponent(entityType)}`
      );
      const next: EntityLabelMap = {};
      for (const record of Array.isArray(records) ? records : []) {
        next[record.entity_id] = Array.isArray(record.labels) ? record.labels : [];
      }
      setLabelsState(next);
    } catch {
      setLabelsState({});
    }
  };

  useEffect(() => {
    void reload();
  }, [entityType]);

  const setEntityLabels = (entityID: string, next: string[]) => {
    setLabelsState((current) => ({ ...current, [entityID]: next }));
  };

  return { labels, reload, setEntityLabels };
}

type EntityLabelsProps = {
  entityType: string;
  entityID: string | number;
  name: string;
  labels: string[] | undefined;
  onSaved: (labels: string[]) => void;
};

export function EntityLabels({
  entityType,
  entityID,
  name,
  labels,
  onSaved,
}: EntityLabelsProps) {
  const [open, setOpen] = useState(false);
  const current = Array.isArray(labels) ? labels : [];
  return (
    <div class="entity-labels">
      {current.map((label) => (
        <span class="label-chip" key={label} title={label}>
          {label}
        </span>
      ))}
      <button
        class="label-add"
        type="button"
        aria-label={`Edit labels for ${name}`}
        onClick={() => setOpen(true)}
      >
        {current.length ? "edit labels" : "+ label"}
      </button>
      {open ? (
        <LabelsDialog
          entityType={entityType}
          entityID={String(entityID)}
          name={name}
          labels={current}
          onClose={() => setOpen(false)}
          onSaved={(next) => {
            onSaved(next);
            setOpen(false);
          }}
        />
      ) : null}
    </div>
  );
}

type LabelsDialogProps = {
  entityType: string;
  entityID: string;
  name: string;
  labels: string[];
  onClose: () => void;
  onSaved: (labels: string[]) => void;
};

function LabelsDialog({
  entityType,
  entityID,
  name,
  labels,
  onClose,
  onSaved,
}: LabelsDialogProps) {
  const dialogRef = useRef<HTMLDialogElement | null>(null);
  const inputRef = useRef<HTMLInputElement | null>(null);
  const [items, setItems] = useState<string[]>(labels);
  const [input, setInput] = useState("");
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    dialogRef.current?.showModal();
    inputRef.current?.focus();
  }, []);

  const close = () => {
    if (!saving) {
      dialogRef.current?.close();
    }
  };

  const add = () => {
    const values = input
      .split(/[\n,]+/)
      .map((value) => value.trim())
      .filter(Boolean);
    if (values.length === 0) {
      return;
    }
    setItems((current) => {
      const next = [...current];
      for (const value of values) {
        if (!next.includes(value)) {
          next.push(value);
        }
      }
      return next;
    });
    setInput("");
    setError("");
  };

  const save = async (event: Event) => {
    event.preventDefault();
    if (saving) {
      return;
    }
    setSaving(true);
    setError("");
    try {
      const updated = await api<EntityLabelsRecord>(
        `/api/labels/${encodeURIComponent(entityType)}/${encodeURIComponent(entityID)}`,
        {
          method: "PUT",
          body: JSON.stringify({ labels: items }),
        }
      );
      onSaved(Array.isArray(updated.labels) ? updated.labels : []);
      showToast(`Saved labels for ${name}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  };

  return (
    <dialog
      ref={dialogRef}
      class="dialog labels-dialog"
      aria-labelledby="labels-dialog-title"
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
      <form onSubmit={(event) => void save(event)}>
        <header class="dialog-head">
          <div>
            <p class="eyebrow">Labels</p>
            <h2 id="labels-dialog-title">{name}</h2>
          </div>
          <button
            class="icon-button"
            type="button"
            aria-label="Close dialog"
            onClick={close}
          >
            ×
          </button>
        </header>
        <div class="labels-editor">
          <span class="field-hint">
            Add as many labels as you need. Labels are free-form text; commas separate multiple
            labels. Up to {MAX_LABELS} per {entityType}.
          </span>
          <div class="labels-editor-list">
            {items.length ? (
              items.map((label) => (
                <span class="label-chip editable" key={label}>
                  {label}
                  <button
                    type="button"
                    aria-label={`Remove ${label}`}
                    disabled={saving}
                    onClick={() => setItems((current) => current.filter((item) => item !== label))}
                  >
                    ×
                  </button>
                </span>
              ))
            ) : (
              <span class="field-hint">No labels yet.</span>
            )}
          </div>
          <div class="labels-editor-input">
            <input
              ref={inputRef}
              type="text"
              value={input}
              maxLength={256}
              placeholder="production, web, critical"
              onInput={(event) => setInput(event.currentTarget.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter") {
                  event.preventDefault();
                  add();
                }
              }}
            />
            <button class="button ghost" type="button" disabled={!input.trim() || saving} onClick={add}>
              Add
            </button>
          </div>
          {error ? (
            <p class="form-error" role="alert">
              {error}
            </p>
          ) : null}
        </div>
        <footer class="dialog-actions">
          <button class="button ghost" type="button" disabled={saving} onClick={close}>
            Cancel
          </button>
          <button class="button primary" type="submit" disabled={saving}>
            {saving ? "Saving…" : "Save labels"}
          </button>
        </footer>
      </form>
    </dialog>
  );
}
