import { useEffect, useState } from "preact/hooks";
import { api } from "../../shared/api";
import { LabelsInput, saveEntityLabelsWithToast, useEntityLabelDraft } from "../../shared/labels";
import type { RouteTable } from "../types";

type RouteTableCreatePageProps = {
  tables: RouteTable[];
  onCancel: () => void;
  onSaved: (table: RouteTable) => void;
};

export function RouteTableCreatePage({ tables, onCancel, onSaved }: RouteTableCreatePageProps) {
  const nextID = Math.max(0, ...tables.filter((table) => !table.is_default).map((table) => table.table_id)) + 1;
  const [name, setName] = useState("");
  const [tableID, setTableID] = useState(String(nextID));
  const [ipv6, setIPv6] = useState(false);
  const [labels, setLabels] = useEntityLabelDraft("route_table");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    document.title = "Create route table · rookery";
  }, []);

  const submit = async (event: Event) => {
    event.preventDefault();
    if (saving) {
      return;
    }
    setError("");
    setSaving(true);
    try {
      const saved = await api<RouteTable>("/api/route-tables", {
        method: "POST",
        body: JSON.stringify({
          name: name.trim(),
          table_id: Number(tableID),
          ipv6,
        }),
      });
      await saveEntityLabelsWithToast("route_table", saved.id, saved.name, labels);
      onSaved(saved);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div class="route-page">
      <header class="route-page-head">
        <div>
          <button class="back-link" type="button" onClick={onCancel}>
            ← Route tables
          </button>
          <p class="eyebrow">IP FIB / VRF</p>
          <h2>Create route table</h2>
          <p class="panel-subtitle">
            Define an IPv4 or IPv6 VRF/FIB table. Table 0 is the built-in default VRF.
          </p>
        </div>
      </header>

      <form class="route-page-form" novalidate onSubmit={(event) => void submit(event)}>
        <div class="form-grid">
          <label class="field">
            <span class="field-label">Name</span>
            <input
              type="text"
              required
              maxLength={63}
              placeholder="tenant-blue"
              value={name}
              onInput={(event) => setName(event.currentTarget.value)}
            />
          </label>
          <label class="field">
            <span class="field-label">Table ID</span>
            <input
              type="number"
              required
              min={1}
              value={tableID}
              onInput={(event) => setTableID(event.currentTarget.value)}
            />
            <span class="field-hint">Custom tables start at 1; table 0 is the default VRF.</span>
          </label>
          <label class="field field-wide checkbox-field">
            <span class="checkbox-row">
              <input type="checkbox" checked={ipv6} onChange={(event) => setIPv6(event.currentTarget.checked)} />
              <span>IPv6 FIB table</span>
            </span>
            <span class="field-hint">The same table ID can be used separately for IPv4 and IPv6.</span>
          </label>
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
          <button class="button ghost" type="button" disabled={saving} onClick={onCancel}>
            Cancel
          </button>
          <button class="button primary" type="submit" disabled={saving || !name.trim() || Number(tableID) < 1}>
            {saving ? "Creating…" : "Create route table"}
          </button>
        </footer>
      </form>
    </div>
  );
}
