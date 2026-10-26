import { useEffect, useRef, useState } from "preact/hooks";
import { api } from "../../shared/api";
import type { RouteTable, VPPInterfaceRouteTables } from "../types";

type VPPInterfaceRouteTablesDialogProps = {
  name: string;
  label: string;
  tables: RouteTable[];
  onClose: () => void;
  onSaved: (tables: VPPInterfaceRouteTables) => void;
};

export function VPPInterfaceRouteTablesDialog({
  name,
  label,
  tables,
  onClose,
  onSaved,
}: VPPInterfaceRouteTablesDialogProps) {
  const [ipv4, setIPv4] = useState("0");
  const [ipv6, setIPv6] = useState("0");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const dialogRef = useRef<HTMLDialogElement | null>(null);
  const endpoint = `/api/vpp/interfaces/${encodeURIComponent(name)}/route-tables`;

  useEffect(() => {
    dialogRef.current?.showModal();
    void load();
  }, []);

  const load = async () => {
    setLoading(true);
    try {
      const data = await api<VPPInterfaceRouteTables>(endpoint);
      setIPv4(String(data.ipv4_table_id ?? 0));
      setIPv6(String(data.ipv6_table_id ?? 0));
      setError("");
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  };

  const close = () => {
    if (!saving) {
      dialogRef.current?.close();
    }
  };

  const submit = async (event: Event) => {
    event.preventDefault();
    if (loading || saving) {
      return;
    }
    setSaving(true);
    setError("");
    try {
      const updated = await api<VPPInterfaceRouteTables>(endpoint, {
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
    tables.filter((table) => table.ipv6 === familyIPv6 && table.table_id !== 0);

  return (
    <dialog
      ref={dialogRef}
      id="vpp-interface-route-tables-dialog"
      aria-labelledby="vpp-interface-route-tables-title"
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
            <h2 id="vpp-interface-route-tables-title">Route tables · {name}</h2>
          </div>
          <button class="icon-button" type="button" aria-label="Close dialog" onClick={close}>
            ×
          </button>
        </header>

        <p class="dialog-lede">
          {label}. Table 0 is the default VRF. Saving an explicit binding,
          including default (0), overrides any automatic network VRF for a host
          tap and may affect reachability.
        </p>

        <div class="form-grid">
          <label class="field">
            <span class="field-label">IPv4 FIB</span>
            <select
              value={ipv4}
              disabled={loading || saving}
              onChange={(event) => setIPv4(event.currentTarget.value)}
            >
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
            <select
              value={ipv6}
              disabled={loading || saving}
              onChange={(event) => setIPv6(event.currentTarget.value)}
            >
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
          <button class="button primary" type="submit" disabled={loading || saving}>
            {saving ? "Applying…" : "Apply route tables"}
          </button>
        </footer>
      </form>
    </dialog>
  );
}
