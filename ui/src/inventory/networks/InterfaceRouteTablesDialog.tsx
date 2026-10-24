import { useEffect, useRef, useState } from "preact/hooks";
import { api } from "../../shared/api";
import type { RouteTable, VirtualInterface } from "../types";

type InterfaceRouteTablesDialogProps = {
  iface: VirtualInterface;
  tables: RouteTable[];
  onClose: () => void;
  onSaved: (iface: VirtualInterface) => void;
};

export function InterfaceRouteTablesDialog({
  iface,
  tables,
  onClose,
  onSaved,
}: InterfaceRouteTablesDialogProps) {
  const [ipv4, setIPv4] = useState(String(iface.ipv4_table_id ?? 0));
  const [ipv6, setIPv6] = useState(String(iface.ipv6_table_id ?? 0));
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
      const updated = await api<VirtualInterface>(`/api/interfaces/${iface.id}/route-tables`, {
        method: "PUT",
        body: JSON.stringify({
          ipv4_table_id: Number(ipv4),
          ipv6_table_id: Number(ipv6),
        }),
      });
      onSaved(updated);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  };

  const options = (familyIPv6: boolean) =>
    tables.filter((table) => table.ipv6 === familyIPv6);

  return (
    <dialog
      ref={dialogRef}
      id="interface-route-tables-dialog"
      aria-labelledby="interface-route-tables-title"
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
            <p class="eyebrow">Interface FIB binding</p>
            <h2 id="interface-route-tables-title">Route tables · {iface.name}</h2>
          </div>
          <button class="icon-button" type="button" aria-label="Close dialog" onClick={close}>
            ×
          </button>
        </header>

        <p class="dialog-lede">
          {iface.network_name} · {iface.mac}. Table 0 is the default VRF.
        </p>

        <div class="form-grid">
          <label class="field">
            <span class="field-label">IPv4 FIB</span>
            <select value={ipv4} onChange={(event) => setIPv4(event.currentTarget.value)}>
              <option value="0">default (0)</option>
              {options(false).map((table) => (
                <option value={String(table.table_id)} key={`v4-${table.id}`}>
                  {table.name} ({table.table_id})
                </option>
              ))}
            </select>
          </label>
          <label class="field">
            <span class="field-label">IPv6 FIB</span>
            <select value={ipv6} onChange={(event) => setIPv6(event.currentTarget.value)}>
              <option value="0">default (0)</option>
              {options(true).map((table) => (
                <option value={String(table.table_id)} key={`v6-${table.id}`}>
                  {table.name} ({table.table_id})
                </option>
              ))}
            </select>
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
            {saving ? "Applying…" : "Apply route tables"}
          </button>
        </footer>
      </form>
    </dialog>
  );
}
