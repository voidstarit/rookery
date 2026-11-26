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

/**
 * useEntityLabels loads the label map for one entity type, exposes a reload function, and merges
 * rookery:labels-changed events; any fetch failure falls back to an empty map.
 */
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

  useEffect(() => {
    const onLabelsChanged = (event: Event) => {
      const detail = (event as CustomEvent).detail as {
        entityType?: string;
        entityID?: string;
        labels?: string[];
      };
      if (!detail || detail.entityType !== entityType || !detail.entityID) {
        return;
      }
      setLabelsState((current) => ({
        ...current,
        [detail.entityID as string]: Array.isArray(detail.labels) ? detail.labels : [],
      }));
    };
    window.addEventListener("rookery:labels-changed", onLabelsChanged);
    return () => window.removeEventListener("rookery:labels-changed", onLabelsChanged);
  }, [entityType]);

  const setEntityLabels = (entityID: string, next: string[]) => {
    setLabelsState((current) => ({ ...current, [entityID]: next }));
  };

  return { labels, reload, setEntityLabels };
}

/**
 * useEntityLabelDraft fetches the labels for one entity into editable state and returns
 * [labels, setLabels]; an absent entity ID yields an empty list and stale responses are ignored.
 */
export function useEntityLabelDraft(entityType: string, entityID?: string | number) {
  const [labels, setLabels] = useState<string[]>([]);
  useEffect(() => {
    if (entityID === undefined || entityID === null || String(entityID) === "") {
      setLabels([]);
      return;
    }
    let active = true;
    api<EntityLabelsRecord>(
      `/api/labels/${encodeURIComponent(entityType)}/${encodeURIComponent(String(entityID))}`
    )
      .then((record) => {
        if (active) {
          setLabels(Array.isArray(record.labels) ? record.labels : []);
        }
      })
      .catch(() => {
        if (active) {
          setLabels([]);
        }
      });
    return () => {
      active = false;
    };
  }, [entityType, entityID]);
  return [labels, setLabels] as const;
}

/**
 * saveEntityLabels PUTs the full label list, broadcasts rookery:labels-changed with the server's
 * normalized labels, and returns them to the caller.
 */
export async function saveEntityLabels(
  entityType: string,
  entityID: string | number,
  labels: string[]
): Promise<string[]> {
  const updated = await api<EntityLabelsRecord>(
    `/api/labels/${encodeURIComponent(entityType)}/${encodeURIComponent(String(entityID))}`,
    {
      method: "PUT",
      body: JSON.stringify({ labels }),
    }
  );
  const next = Array.isArray(updated.labels) ? updated.labels : [];
  window.dispatchEvent(
    new CustomEvent("rookery:labels-changed", {
      detail: { entityType, entityID: String(entityID), labels: next },
    })
  );
  return next;
}

/**
 * saveEntityLabelsWithToast persists labels without throwing, reporting a failed update as an
 * error toast instead.
 */
export async function saveEntityLabelsWithToast(
  entityType: string,
  entityID: string | number,
  name: string,
  labels: string[]
): Promise<void> {
  try {
    await saveEntityLabels(entityType, entityID, labels);
  } catch (err) {
    showToast(
      `Saved ${name}, but labels could not be updated: ${
        err instanceof Error ? err.message : String(err)
      }`,
      true
    );
  }
}

type LabelsInputProps = {
  labels: string[];
  onChange: (labels: string[]) => void;
  disabled?: boolean;
};

/**
 * LabelsInput renders a removable chip editor for a label list and calls onChange with the
 * deduplicated result; comma- or newline-separated entries are added and the cap is 64 labels.
 */
export function LabelsInput({ labels, onChange, disabled = false }: LabelsInputProps) {
  const [input, setInput] = useState("");
  const values = Array.isArray(labels) ? labels : [];

  const add = () => {
    const additions = input
      .split(/[\n,]+/)
      .map((value) => value.trim())
      .filter(Boolean);
    if (additions.length === 0) {
      return;
    }
    const next = [...values];
    for (const value of additions) {
      if (!next.includes(value)) {
        next.push(value);
      }
    }
    onChange(next);
    setInput("");
  };

  return (
    <div class="labels-field">
      <span class="field-label">Labels</span>
      <div class="labels-editor-list">
        {values.length ? (
          values.map((label) => (
            <span class="label-chip editable" key={label}>
              {label}
              <button
                type="button"
                aria-label={`Remove ${label}`}
                disabled={disabled}
                onClick={() => onChange(values.filter((item) => item !== label))}
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
          type="text"
          value={input}
          maxLength={256}
          disabled={disabled || values.length >= MAX_LABELS}
          placeholder="production, web, critical"
          onInput={(event) => setInput(event.currentTarget.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter") {
              event.preventDefault();
              add();
            }
          }}
        />
        <button
          class="button ghost"
          type="button"
          disabled={disabled || !input.trim() || values.length >= MAX_LABELS}
          onClick={add}
        >
          Add
        </button>
      </div>
      <span class="field-hint">
        Add as many labels as you need. Commas separate multiple labels; up to {MAX_LABELS}.
      </span>
    </div>
  );
}

type EntityLabelsProps = {
  entityType: string;
  entityID: string | number;
  name: string;
  labels: string[] | undefined;
  onSaved: (labels: string[]) => void;
};

/**
 * EntityLabels renders read-only label chips plus an edit button that opens LabelsDialog, and
 * forwards a successful save through onSaved.
 */
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

/**
 * LabelsDialog renders the modal label editor, blocks closing while a save is in flight, and
 * calls onSaved after the PUT succeeds; API failures appear inline instead of closing.
 */
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
      const next = await saveEntityLabels(entityType, entityID, items);
      onSaved(next);
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
