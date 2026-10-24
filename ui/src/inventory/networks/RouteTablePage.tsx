import { useEffect, useState } from "preact/hooks";
import { api } from "../../shared/api";
import type { FIBRoute, Route, RouteTable, VirtualInterface } from "../types";

type RouteTablePageProps = {
  table: RouteTable;
  interfaces: VirtualInterface[];
  bviInterfaces: string[];
  onBack: () => void;
  onChanged: () => void;
};

export function RouteTablePage({ table, interfaces, bviInterfaces, onBack, onChanged }: RouteTablePageProps) {
  const [routes, setRoutes] = useState<Route[]>([]);
  const [fibRoutes, setFibRoutes] = useState<FIBRoute[]>([]);
  const [loading, setLoading] = useState(true);
  const [fibLoading, setFibLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [fibError, setFibError] = useState("");
  const [prefix, setPrefix] = useState(table.ipv6 ? "2001:db8::/64" : "10.0.0.0/24");
  const [nextHop, setNextHop] = useState(table.ipv6 ? "2001:db8::1" : "10.0.0.1");
  const [egress, setEgress] = useState("");
  const [preference, setPreference] = useState("");

  const loadFIB = async () => {
    setFibLoading(true);
    try {
      const data = await api<FIBRoute[]>(`/api/route-tables/${table.id}/fib?ipv6=${table.ipv6}`);
      setFibRoutes(Array.isArray(data) ? data : []);
      setFibError("");
    } catch (err) {
      setFibError(err instanceof Error ? err.message : String(err));
    } finally {
      setFibLoading(false);
    }
  };

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    api<Route[]>(`/api/route-tables/${table.id}/routes?ipv6=${table.ipv6}`)
      .then((data) => {
        if (!cancelled) {
          setRoutes(Array.isArray(data) ? data : []);
          setError("");
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : String(err));
        }
      })
      .finally(() => {
        if (!cancelled) {
          setLoading(false);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [table.id, table.ipv6]);

  useEffect(() => {
    void loadFIB();
  }, [table.id, table.ipv6]);

  useEffect(() => {
    document.title = `Routes · ${table.name} · rookery`;
  }, [table.name]);

  const addRoute = async (event: Event) => {
    event.preventDefault();
    if (saving) {
      return;
    }
    if (!prefix.trim()) {
      setError("Enter a destination prefix.");
      return;
    }
    if (!nextHop.trim() && !egress) {
      setError("Enter a next hop or select an egress interface for an attached route.");
      return;
    }
    setError("");
    setSaving(true);
    try {
      const egressName = egress.startsWith("bvi:") ? egress.slice(4) : "";
      const ifaceID = egress && !egressName ? Number(egress) : 0;
      await api<Route>(`/api/route-tables/${table.id}/routes`, {
        method: "POST",
        body: JSON.stringify({
          ipv6: table.ipv6,
          prefix: prefix.trim(),
          next_hop: nextHop.trim(),
          ...(ifaceID ? { interface_id: ifaceID } : {}),
          ...(egressName ? { egress_interface: egressName } : {}),
          preference: Math.max(0, Math.trunc(Number(preference) || 0)),
        }),
      });
      const refreshed = await api<Route[]>(`/api/route-tables/${table.id}/routes?ipv6=${table.ipv6}`);
      setRoutes(Array.isArray(refreshed) ? refreshed : []);
      await loadFIB();
      onChanged();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  };

  const deleteRoute = async (route: Route) => {
    if (!window.confirm(`Delete route ${route.prefix} via ${route.next_hop}?`)) {
      return;
    }
    setError("");
    try {
      await api(`/api/routes/${route.id}`, { method: "DELETE" });
      setRoutes((current) => current.filter((item) => item.id !== route.id));
      await loadFIB();
      onChanged();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  const dynamicRoutes = fibRoutes.filter((route) => route.dynamic);

  return (
    <div class="route-page">
      <header class="route-page-head">
        <div>
          <button class="back-link" type="button" onClick={onBack}>
            ← Route tables
          </button>
          <p class="eyebrow">
            {table.ipv6 ? "IPv6" : "IPv4"} FIB · table {table.table_id}
            {table.is_default ? " · default VRF" : ""}
          </p>
          <h2>Routes · {table.name}</h2>
          <p class="panel-subtitle">
            Static routes are managed by Rookery. Dynamic routes are read directly from the live VPP FIB.
          </p>
        </div>
      </header>

      <section class="route-page-section" aria-labelledby="static-routes-title">
        <div class="panel-head">
          <div>
            <h3 id="static-routes-title">Static routes</h3>
            <p class="field-hint">Prefixes installed in this VRF by Rookery.</p>
          </div>
        </div>

        <form class="route-add-form" novalidate onSubmit={(event) => void addRoute(event)}>
          <label class="field">
            <span class="field-label">Prefix</span>
            <input
              type="text"
              required
              placeholder={table.ipv6 ? "2001:db8::/64" : "10.0.0.0/24"}
              value={prefix}
              onInput={(event) => setPrefix(event.currentTarget.value)}
            />
          </label>
          <label class="field">
            <span class="field-label">Next hop</span>
            <input
              type="text"
              placeholder={egress ? "optional for attached route" : table.ipv6 ? "2001:db8::1" : "10.0.0.1"}
              value={nextHop}
              onInput={(event) => setNextHop(event.currentTarget.value)}
            />
          </label>
          <label class="field">
            <span class="field-label">Egress interface</span>
            <select value={egress} onChange={(event) => setEgress(event.currentTarget.value)}>
              <option value="">Resolve via table</option>
              {interfaces.map((iface) => (
                <option value={String(iface.id)} key={iface.id}>
                  {iface.name}
                </option>
              ))}
              {bviInterfaces.length > 0 ? (
                <optgroup label="Network gateways (BVI)">
                  {bviInterfaces.map((name) => (
                    <option value={`bvi:${name}`} key={name}>
                      {name}
                    </option>
                  ))}
                </optgroup>
              ) : null}
            </select>
          </label>
          <label class="field">
            <span class="field-label">Metric / preference</span>
            <input
              type="number"
              min="0"
              max="255"
              step="1"
              placeholder="0"
              value={preference}
              onInput={(event) => setPreference(event.currentTarget.value)}
            />
          </label>
          <button
            class="button primary"
            type="submit"
            disabled={saving}
          >
            {saving ? "Adding…" : "Add route"}
          </button>
        </form>

        {error ? (
          <p class="form-error" role="alert">
            {error}
          </p>
        ) : null}

        <div class="table-wrap route-table-wrap">
          {loading ? (
            <p class="empty-state">Loading routes…</p>
          ) : routes.length > 0 ? (
            <table>
              <thead>
                <tr>
                  <th scope="col">Prefix</th>
                  <th scope="col">Next hop</th>
                  <th scope="col">Interface</th>
                  <th scope="col">Preference</th>
                  <th scope="col">
                    <span class="sr-only">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {routes.map((route) => (
                  <tr key={route.id}>
                    <td class="mono">{route.prefix}</td>
                    <td class="mono">{route.next_hop || "attached"}</td>
                    <td class="mono">
                      {route.egress_interface || route.interface_name || "resolved by table"}
                    </td>
                    <td class="mono">{route.preference || 0}</td>
                    <td>
                      <button class="row-action danger" type="button" onClick={() => void deleteRoute(route)}>
                        Delete
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : (
            <p class="empty-state">No static routes in this table yet.</p>
          )}
        </div>
      </section>

      <section class="fib-section route-page-section" aria-labelledby="fib-routes-title">
        <div class="panel-head fib-head">
          <div>
            <h3 id="fib-routes-title">Dynamic routes</h3>
            <p class="field-hint">
              Read-only entries from the live VPP FIB, including connected, DHCP, ND, and adjacency routes.
            </p>
          </div>
          <button class="button ghost" type="button" disabled={fibLoading} onClick={() => void loadFIB()}>
            {fibLoading ? "Refreshing…" : "Refresh FIB"}
          </button>
        </div>

        {fibError ? (
          <p class="form-error" role="alert">
            {fibError}
          </p>
        ) : fibLoading ? (
          <p class="empty-state">Loading VPP FIB…</p>
        ) : dynamicRoutes.length > 0 ? (
          <div class="table-wrap route-table-wrap">
            <table>
              <thead>
                <tr>
                  <th scope="col">Prefix</th>
                  <th scope="col">Source</th>
                  <th scope="col">Paths</th>
                </tr>
              </thead>
              <tbody>
                {dynamicRoutes.map((route) => (
                  <tr key={`${route.ipv6 ? "v6" : "v4"}-${route.prefix}`}>
                    <td class="mono">{route.prefix}</td>
                    <td>
                      <span class="fib-source">{route.source_name}</span>
                    </td>
                    <td>
                      {(route.paths ?? []).length > 0 ? (
                        (route.paths ?? []).map((path, index) => (
                          <div class="fib-path" key={`${route.prefix}-${index}`}>
                            <span class="fib-path-type">{path.type}</span>{" "}
                            <span class="mono">{path.next_hop || "—"}</span>
                            {path.interface_name ? <span> via {path.interface_name}</span> : null}
                          </div>
                        ))
                      ) : (
                        <span class="muted">no paths</span>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <p class="empty-state">No dynamic routes in this table.</p>
        )}
      </section>
    </div>
  );
}
