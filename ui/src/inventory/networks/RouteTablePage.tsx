import { useEffect, useState } from "preact/hooks";
import { api } from "../../shared/api";
import { EntityLabels, useEntityLabels } from "../../shared/labels";
import { navigate } from "../router";
import type { FIBRoute, Route, RouteTable } from "../types";

type RouteTablePageProps = {
  table: RouteTable;
  onBack: () => void;
  onChanged: () => void;
};

function routeOptions(route: Route): string {
  const parts: string[] = [];
  if ((route.count || 0) > 1) parts.push(`count ${route.count}`);
  if (route.next_hop_table) parts.push(`next-hop-table ${route.next_hop_table}`);
  if (route.weight) parts.push(`weight ${route.weight}`);
  if (route.udp_encap) parts.push(`udp-encap ${route.udp_encap}`);
  if (route.ipv4_lookup_table) parts.push(`ip4-lookup-in-table ${route.ipv4_lookup_table}`);
  if (route.ipv6_lookup_table) parts.push(`ip6-lookup-in-table ${route.ipv6_lookup_table}`);
  if (route.mpls_lookup_table) parts.push(`mpls-lookup-in-table ${route.mpls_lookup_table}`);
  if (route.resolve_via_host) parts.push("resolve-via-host");
  if (route.resolve_via_connected) parts.push("resolve-via-connected");
  if (route.rx_ipv4_interface) parts.push(`rx-ip4 ${route.rx_ipv4_interface}`);
  if (route.rx_ipv6_interface) parts.push(`rx-ip6 ${route.rx_ipv6_interface}`);
  if (route.out_labels && route.out_labels.length > 0) parts.push(`out-labels ${route.out_labels.join(" ")}`);
  if (route.drop) parts.push("drop");
  return parts.join(" · ");
}

export function RouteTablePage({ table, onBack, onChanged }: RouteTablePageProps) {
  const [routes, setRoutes] = useState<Route[]>([]);
  const [fibRoutes, setFibRoutes] = useState<FIBRoute[]>([]);
  const [loading, setLoading] = useState(true);
  const [fibLoading, setFibLoading] = useState(true);
  const [error, setError] = useState("");
  const [fibError, setFibError] = useState("");
  const routeLabels = useEntityLabels("route");
  const formPath = `/route-tables/${table.id}/routes/new?ipv6=${table.ipv6}`;

  const loadRoutes = async () => {
    setLoading(true);
    try {
      const data = await api<Route[]>(`/api/route-tables/${table.id}/routes?ipv6=${table.ipv6}`);
      setRoutes(Array.isArray(data) ? data : []);
      setError("");
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  };

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
    void loadRoutes();
  }, [table.id, table.ipv6]);

  useEffect(() => {
    void loadFIB();
  }, [table.id, table.ipv6]);

  useEffect(() => {
    document.title = `Routes · ${table.name} · rookery`;
  }, [table.name]);

  const deleteRoute = async (route: Route) => {
    if (!window.confirm(`Delete route ${route.prefix}${route.next_hop ? ` via ${route.next_hop}` : ""}?`)) {
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
        <button class="button primary" type="button" onClick={() => navigate(formPath)}>
          <span class="button-plus" aria-hidden="true">+</span>
          Add route
        </button>
      </header>

      <section class="route-page-section" aria-labelledby="static-routes-title">
        <div class="panel-head">
          <div>
            <h3 id="static-routes-title">Static routes</h3>
            <p class="field-hint">Prefixes installed in this VRF by Rookery.</p>
          </div>
        </div>

        {error ? <p class="form-error" role="alert">{error}</p> : null}

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
                  <th scope="col">Options</th>
                  <th scope="col">Preference</th>
                  <th scope="col"><span class="sr-only">Actions</span></th>
                </tr>
              </thead>
              <tbody>
                {routes.map((route) => (
                  <tr key={route.id}>
                    <td>
                      <span class="vm-name mono">{route.prefix}</span>
                      {route.description ? <span class="vm-description">{route.description}</span> : null}
                      <EntityLabels
                        entityType="route"
                        entityID={route.id}
                        name={route.prefix}
                        labels={routeLabels.labels[String(route.id)]}
                        onSaved={(labels) => routeLabels.setEntityLabels(String(route.id), labels)}
                      />
                    </td>
                    <td class="mono">{route.drop ? "drop" : route.next_hop || "attached"}</td>
                    <td class="mono">{route.egress_interface || route.interface_name || "resolved by table"}</td>
                    <td class="mono">{routeOptions(route) || "—"}</td>
                    <td class="mono">{route.preference || 0}</td>
                    <td>
                      <div class="actions">
                        <button
                          class="row-action"
                          type="button"
                          onClick={() => navigate(`/route-tables/${table.id}/routes/${route.id}/edit?ipv6=${table.ipv6}`)}
                        >
                          Edit
                        </button>
                        <button class="row-action danger" type="button" onClick={() => void deleteRoute(route)}>
                          Delete
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : (
            <div class="empty">
              <div class="empty-mark" aria-hidden="true">↗</div>
              <h3>No static routes</h3>
              <p>Add a route to this VRF.</p>
              <button class="button primary" type="button" onClick={() => navigate(formPath)}>
                Add route
              </button>
            </div>
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
          <p class="form-error" role="alert">{fibError}</p>
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
                    <td><span class="fib-source">{route.source_name}</span></td>
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
