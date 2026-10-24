import { useEffect, useRef, useState } from "preact/hooks";
import { api } from "../../shared/api";
import type { ACL, VirtualInterface } from "../types";
import { aclByName, formatACLText } from "./acl";

type InterfaceACLDialogProps = {
  iface: VirtualInterface;
  acls: ACL[];
  vppReady: boolean;
  onClose: () => void;
  onSaved: (iface: VirtualInterface) => void;
};

type ACLListEditorProps = {
  label: string;
  hint: string;
  acls: ACL[];
  selected: string[];
  onChange: (next: string[]) => void;
};

function ACLListEditor({ label, hint, acls, selected, onChange }: ACLListEditorProps) {
  const [pending, setPending] = useState("");
  const available = acls.filter((acl) => !selected.includes(acl.name));
  const add = () => {
    if (!pending) {
      return;
    }
    onChange([...selected, pending]);
    setPending("");
  };
  const move = (index: number, offset: number) => {
    const target = index + offset;
    if (target < 0 || target >= selected.length) {
      return;
    }
    const next = selected.slice();
    [next[index], next[target]] = [next[target], next[index]];
    onChange(next);
  };

  return (
    <div class="field field-wide">
      <span class="field-label">{label}</span>
      <div class="acl-list-editor">
        <div class="acl-list-picker">
          <select
            value={pending}
            aria-label={`Select ${label} ACL`}
            onChange={(event) => setPending(event.currentTarget.value)}
          >
            <option value="">Select ACL…</option>
            {available.map((acl) => (
              <option value={acl.name} key={acl.id}>
                {acl.title ? `${acl.name} — ${acl.title}` : acl.name}
              </option>
            ))}
          </select>
          <button class="button ghost" type="button" disabled={!pending} onClick={add}>
            Add
          </button>
        </div>
        <div class="acl-list-selected" aria-label={`Selected ${label} ACL order`}>
          {selected.length === 0 ? (
            <span class="muted">None</span>
          ) : (
            selected.map((name, index) => (
              <div class="acl-order-row" key={name}>
                <span class="mono">
                  {index + 1}. {name}
                </span>
                <button
                  class="button ghost"
                  type="button"
                  aria-label={`Move ${name} up`}
                  disabled={index === 0}
                  onClick={() => move(index, -1)}
                >
                  ↑
                </button>
                <button
                  class="button ghost"
                  type="button"
                  aria-label={`Move ${name} down`}
                  disabled={index === selected.length - 1}
                  onClick={() => move(index, 1)}
                >
                  ↓
                </button>
                <button
                  class="button ghost danger"
                  type="button"
                  aria-label={`Remove ${name}`}
                  onClick={() => onChange(selected.filter((item) => item !== name))}
                >
                  ×
                </button>
              </div>
            ))
          )}
        </div>
      </div>
      <span class="field-hint">{hint}</span>
    </div>
  );
}

export function InterfaceACLDialog({
  iface,
  acls,
  vppReady,
  onClose,
  onSaved,
}: InterfaceACLDialogProps) {
  const initialIngress = iface.ingress_acls?.length
    ? iface.ingress_acls
    : iface.ingress_acl_name
      ? [iface.ingress_acl_name]
      : [];
  const initialEgress = iface.egress_acls?.length
    ? iface.egress_acls
    : iface.egress_acl_name
      ? [iface.egress_acl_name]
      : [];
  const [ingress, setIngress] = useState<string[]>(initialIngress);
  const [egress, setEgress] = useState<string[]>(initialEgress);
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
    if (saving || !vppReady) {
      return;
    }
    setError("");
    setSaving(true);
    try {
      const updated = await api<VirtualInterface>(`/api/interfaces/${iface.id}/acls`, {
        method: "PUT",
        body: JSON.stringify({ ingress_acls: ingress, egress_acls: egress }),
      });
      onSaved(updated);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  };

  const previewParts: string[] = [];
  const appendPreview = (label: string, names: string[]) => {
    if (names.length === 0) {
      previewParts.push(`${label}: none`);
      return;
    }
    names.forEach((name, index) => {
      const acl = aclByName(acls, name);
      previewParts.push(
        acl
          ? formatACLText(acl, `${label} #${index + 1}`)
          : `${label} #${index + 1}: ${name} (missing)`
      );
    });
  };
  appendPreview("From interface ACL", ingress);
  appendPreview("To interface ACL", egress);
  const preview = previewParts.join("\n\n");

  return (
    <dialog
      ref={dialogRef}
      id="interface-acl-dialog"
      aria-labelledby="interface-acl-title"
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
            <p class="eyebrow">Interface policy</p>
            <h2 id="interface-acl-title">Assign ACLs</h2>
          </div>
          <button class="icon-button" type="button" aria-label="Close dialog" onClick={close}>
            ×
          </button>
        </header>

        <p class="dialog-lede">
          {iface.name} · {iface.network_name} · {iface.mac}
        </p>

        <div class="form-grid">
          <ACLListEditor
            label="From interface"
            hint="Applied to packets entering the interface, in this order. VPP evaluates the first matching rule across the list."
            acls={acls}
            selected={ingress}
            onChange={setIngress}
          />
          <ACLListEditor
            label="To interface"
            hint="Applied to packets leaving the interface, in this order."
            acls={acls}
            selected={egress}
            onChange={setEgress}
          />

          <label class="field field-wide">
            <span class="field-label">Full ACL preview</span>
            <pre class="acl-preview">{preview}</pre>
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
          <button
            class="button primary"
            type="submit"
            disabled={saving || !vppReady}
            title={vppReady ? "" : "VPP must be ready to apply interface ACLs."}
          >
            {saving ? "Applying…" : "Apply ACLs"}
          </button>
        </footer>
      </form>
    </dialog>
  );
}
