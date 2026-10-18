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

export function InterfaceACLDialog({
  iface,
  acls,
  vppReady,
  onClose,
  onSaved,
}: InterfaceACLDialogProps) {
  const [ingress, setIngress] = useState(iface.ingress_acl_name || "");
  const [egress, setEgress] = useState(iface.egress_acl_name || "");
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
        body: JSON.stringify({ ingress_acl: ingress, egress_acl: egress }),
      });
      onSaved(updated);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  };

  const ingressACL = aclByName(acls, ingress);
  const egressACL = aclByName(acls, egress);
  const preview = [
    ingressACL ? formatACLText(ingressACL, "From interface ACL") : "From interface ACL: none",
    egressACL ? formatACLText(egressACL, "To interface ACL") : "To interface ACL: none",
  ].join("\n\n");

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
          <label class="field">
            <span class="field-label">From interface</span>
            <select value={ingress} onChange={(event) => setIngress(event.currentTarget.value)}>
              <option value="">None</option>
              {acls.map((acl) => (
                <option value={acl.name} key={acl.id}>
                  {acl.title ? `${acl.name} — ${acl.title}` : acl.name}
                </option>
              ))}
            </select>
          </label>

          <label class="field">
            <span class="field-label">To interface</span>
            <select value={egress} onChange={(event) => setEgress(event.currentTarget.value)}>
              <option value="">None</option>
              {acls.map((acl) => (
                <option value={acl.name} key={acl.id}>
                  {acl.title ? `${acl.name} — ${acl.title}` : acl.name}
                </option>
              ))}
            </select>
          </label>
        </div>

        <label class="field field-wide">
          <span class="field-label">Full ACL preview</span>
          <pre class="acl-preview">{preview}</pre>
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
