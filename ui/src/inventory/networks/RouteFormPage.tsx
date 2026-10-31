import { useEffect, useState } from "preact/hooks";
import { api } from "../../shared/api";
import { LabelsInput, saveEntityLabelsWithToast, useEntityLabelDraft } from "../../shared/labels";
import type { HostInterface, HostTap, Route, RouteTable, VirtualInterface } from "../types";

type RouteFormPageProps = {
  table: RouteTable;
  routeID?: number;
  interfaces: VirtualInterface[];
  bviInterfaces: string[];
  hostInterfaces: HostInterface[];
  hostTaps: HostTap[];
  tables: RouteTable[];
  onBack: () => void;
  onSaved: () => void;
};

export function RouteFormPage({
  table,
  routeID,
  interfaces,
  bviInterfaces,
  hostInterfaces,
  hostTaps,
  tables,
  onBack,
  onSaved,
}: RouteFormPageProps) {
  const editing = routeID !== undefined;
  const [loading, setLoading] = useState(editing);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [prefix, setPrefix] = useState(table.ipv6 ? "2001:db8::/64" : "10.0.0.0/24");
  const [description, setDescription] = useState("");
  const [nextHop, setNextHop] = useState(table.ipv6 ? "2001:db8::1" : "10.0.0.1");
  const [egress, setEgress] = useState("");
  const [customEgress, setCustomEgress] = useState("");
  const [preference, setPreference] = useState("");
  const [count, setCount] = useState("");
  const [nextHopTable, setNextHopTable] = useState("");
  const [weight, setWeight] = useState("");
  const [udpEncap, setUdpEncap] = useState("");
  const [ipv4LookupTable, setIpv4LookupTable] = useState("");
  const [ipv6LookupTable, setIpv6LookupTable] = useState("");
  const [mplsLookupTable, setMplsLookupTable] = useState("");
  const [resolveViaHost, setResolveViaHost] = useState(false);
  const [resolveViaConnected, setResolveViaConnected] = useState(false);
  const [rxIpv4Interface, setRxIpv4Interface] = useState("");
  const [rxIpv6Interface, setRxIpv6Interface] = useState("");
  const [outLabels, setOutLabels] = useState("");
  const [drop, setDrop] = useState(false);
  const [labels, setLabels] = useEntityLabelDraft("route", routeID);

  useEffect(() => {
    document.title = `${editing ? "Edit" : "Add"} route · ${table.name} · rookery`;
  }, [editing, table.name]);

  useEffect(() => {
    if (routeID === undefined) {
      return;
    }
    let cancelled = false;
    setLoading(true);
    api<Route[]>(`/api/route-tables/${table.id}/routes?ipv6=${table.ipv6}`)
      .then((data) => {
        if (cancelled) return;
        const route = (Array.isArray(data) ? data : []).find((item) => item.id === routeID);
        if (!route) {
          setError("Route not found.");
          return;
        }
        setPrefix(route.prefix);
        setDescription(route.description || "");
        setNextHop(route.next_hop || "");
        if (route.interface_id) {
          setEgress(String(route.interface_id));
          setCustomEgress("");
        } else {
          setEgress("");
          setCustomEgress(route.egress_interface || "");
        }
        setPreference(route.preference === undefined ? "" : String(route.preference));
        setCount(route.count === undefined ? "" : String(route.count));
        setNextHopTable(route.next_hop_table === undefined ? "" : String(route.next_hop_table));
        setWeight(route.weight === undefined ? "" : String(route.weight));
        setUdpEncap(route.udp_encap === undefined ? "" : String(route.udp_encap));
        setIpv4LookupTable(route.ipv4_lookup_table === undefined ? "" : String(route.ipv4_lookup_table));
        setIpv6LookupTable(route.ipv6_lookup_table === undefined ? "" : String(route.ipv6_lookup_table));
        setMplsLookupTable(route.mpls_lookup_table === undefined ? "" : String(route.mpls_lookup_table));
        setResolveViaHost(Boolean(route.resolve_via_host));
        setResolveViaConnected(Boolean(route.resolve_via_connected));
        setRxIpv4Interface(route.rx_ipv4_interface || "");
        setRxIpv6Interface(route.rx_ipv6_interface || "");
        setOutLabels((route.out_labels || []).join(" "));
        setDrop(Boolean(route.drop));
        setError("");
      })
      .catch((err: unknown) => setError(err instanceof Error ? err.message : String(err)))
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [routeID, table.id, table.ipv6]);

  const submit = async (event: Event) => {
    event.preventDefault();
    if (saving || loading) return;
    if (!prefix.trim()) {
      setError("Enter a destination prefix.");
      return;
    }
    if (rxIpv4Interface.trim() && rxIpv6Interface.trim()) {
      setError("rx-ip4 and rx-ip6 are mutually exclusive.");
      return;
    }
    const hasPath =
      nextHop.trim() ||
      egress ||
      customEgress.trim() ||
      drop ||
      rxIpv4Interface.trim() ||
      rxIpv6Interface.trim() ||
      nextHopTable ||
      udpEncap ||
      ipv4LookupTable ||
      ipv6LookupTable ||
      mplsLookupTable;
    if (!hasPath) {
      setError("Enter a next hop, egress interface, rx interface, lookup option, or drop.");
      return;
    }
    const selectedEgressName = egress.startsWith("vpp:") ? egress.slice(4) : "";
    const egressName = customEgress.trim() || selectedEgressName;
    const ifaceID = egress && !selectedEgressName && !customEgress.trim() ? Number(egress) : 0;
    const outLabelValues = outLabels
      .split(/[\s,]+/)
      .map((value) => value.trim())
      .filter(Boolean)
      .map((value) => Math.trunc(Number(value)))
      .filter((value) => Number.isFinite(value) && value >= 0);
    const optionalNumber = (value: string): number | undefined => {
      const trimmed = value.trim();
      if (!trimmed) {
        return undefined;
      }
      const parsed = Math.max(0, Math.trunc(Number(trimmed)));
      return Number.isFinite(parsed) ? parsed : undefined;
    };
    const preferenceValue = optionalNumber(preference);
    const countValue = optionalNumber(count);
    const weightValue = optionalNumber(weight);
    const udpEncapValue = optionalNumber(udpEncap);
    const mplsLookupValue = optionalNumber(mplsLookupTable);
    const nextHopTableValue = nextHopTable.trim();
    const ipv4LookupValue = ipv4LookupTable.trim();
    const ipv6LookupValue = ipv6LookupTable.trim();
    const body = {
      ipv6: table.ipv6,
      prefix: prefix.trim(),
      description: description.trim(),
      next_hop: nextHop.trim(),
      ...(ifaceID ? { interface_id: ifaceID } : {}),
      ...(egressName ? { egress_interface: egressName } : {}),
      ...(preferenceValue !== undefined ? { preference: preferenceValue } : {}),
      ...(countValue !== undefined ? { count: countValue } : {}),
      ...(nextHopTableValue ? { next_hop_table: nextHopTableValue } : {}),
      ...(weightValue !== undefined ? { weight: weightValue } : {}),
      ...(udpEncapValue !== undefined ? { udp_encap: udpEncapValue } : {}),
      ...(ipv4LookupValue ? { ipv4_lookup_table: ipv4LookupValue } : {}),
      ...(ipv6LookupValue ? { ipv6_lookup_table: ipv6LookupValue } : {}),
      ...(mplsLookupValue !== undefined ? { mpls_lookup_table: mplsLookupValue } : {}),
      resolve_via_host: resolveViaHost,
      resolve_via_connected: resolveViaConnected,
      rx_ipv4_interface: rxIpv4Interface.trim(),
      rx_ipv6_interface: rxIpv6Interface.trim(),
      out_labels: outLabelValues,
      drop,
    };
    setSaving(true);
    setError("");
    try {
      let saved: Route;
      if (routeID === undefined) {
        saved = await api<Route>(`/api/route-tables/${table.id}/routes`, {
          method: "POST",
          body: JSON.stringify(body),
        });
      } else {
        const { ipv6: _ipv6, ...updateFields } = body;
        saved = await api<Route>(`/api/routes/${routeID}`, {
          method: "PUT",
          body: JSON.stringify({ id: routeID, ...updateFields }),
        });
      }
      await saveEntityLabelsWithToast("route", saved.id, saved.prefix, labels);
      onSaved();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  };

  const tableOptions = (ipv6: boolean) =>
    tables.filter((item) => item.ipv6 === ipv6 && item.table_id !== 0);

  return (
    <div class="route-page">
      <header class="route-page-head">
        <div>
          <button class="back-link" type="button" onClick={onBack}>
            ← Routes · {table.name}
          </button>
          <p class="eyebrow">
            {table.ipv6 ? "IPv6" : "IPv4"} FIB · table {table.table_id}
          </p>
          <h2>{editing ? "Edit route" : "Add route"}</h2>
          <p class="panel-subtitle">
            Route options map directly to the VPP <code>ip route</code> command.
          </p>
        </div>
      </header>

      <form class="route-page-form" onSubmit={(event) => void submit(event)}>
        {loading ? <p class="field-hint">Loading route…</p> : null}
        <div class="form-grid">
          <label class="field">
            <span class="field-label">Prefix</span>
            <input type="text" required value={prefix} onInput={(event) => setPrefix(event.currentTarget.value)} />
          </label>
          <label class="field">
            <span class="field-label">Next hop</span>
            <input type="text" value={nextHop} onInput={(event) => setNextHop(event.currentTarget.value)} />
          </label>
          <label class="field field-wide">
            <span class="field-label">Egress interface</span>
            <select value={egress} onChange={(event) => setEgress(event.currentTarget.value)}>
              <option value="">Resolve via table</option>
              {interfaces.map((iface) => (
                <option value={String(iface.id)} key={iface.id}>{iface.name}</option>
              ))}
              {bviInterfaces.length > 0 ? (
                <optgroup label="Network gateways (BVI)">
                  {bviInterfaces.map((name) => <option value={`vpp:${name}`} key={`bvi-${name}`}>{name}</option>)}
                </optgroup>
              ) : null}
              {hostInterfaces.some((iface) => iface.in_vpp && iface.vpp_name) ? (
                <optgroup label="Host interfaces">
                  {hostInterfaces.filter((iface) => iface.in_vpp && iface.vpp_name).map((iface) => (
                    <option value={`vpp:${iface.vpp_name}`} key={`host-${iface.name}`}>{iface.vpp_name} (host)</option>
                  ))}
                </optgroup>
              ) : null}
              {hostTaps.length > 0 ? (
                <optgroup label="Host taps">
                  {hostTaps.map((tap) => <option value={`vpp:${tap.name}`} key={`tap-${tap.id}`}>{tap.name}</option>)}
                </optgroup>
              ) : null}
            </select>
          </label>
          <label class="field">
            <span class="field-label">Other VPP interface</span>
            <input type="text" placeholder="host-eno4" value={customEgress} onInput={(event) => setCustomEgress(event.currentTarget.value)} />
            <span class="field-hint">Overrides the picker when set.</span>
          </label>
          <label class="field">
            <span class="field-label">Metric / preference</span>
            <input type="number" min="0" max="255" value={preference} onInput={(event) => setPreference(event.currentTarget.value)} />
          </label>
          <label class="field field-wide">
            <span class="field-label">Description</span>
            <textarea rows={2} maxlength={1000} value={description} onInput={(event) => setDescription(event.currentTarget.value)} />
          </label>
          <div class="field field-wide">
            <LabelsInput labels={labels} onChange={setLabels} disabled={saving} />
          </div>
        </div>

        <section class="route-advanced" aria-labelledby="route-advanced-title">
          <h4 class="route-advanced-title" id="route-advanced-title">Advanced VPP route options</h4>
          <p class="field-hint">
            Leave an option blank to omit it from the VPP command. Next-hop and
            IP lookup tables use route-table names; choose <code>default</code>
            for table <code>0</code>. MPLS lookup uses a numeric table ID.
          </p>
          <div class="route-advanced-grid">
            <label class="field">
              <span class="field-label">Count</span>
              <input type="number" min="1" max="100000" placeholder="1" value={count} onInput={(event) => setCount(event.currentTarget.value)} />
            </label>
            <label class="field">
              <span class="field-label">Next-hop table</span>
              <select value={nextHopTable} onChange={(event) => setNextHopTable(event.currentTarget.value)}>
                <option value="">Omit</option>
                <option value="default">default (0)</option>
                {tableOptions(table.ipv6).map((item) => (
                  <option value={item.name} key={`nh-${item.id}`}>{item.name} ({item.table_id})</option>
                ))}
              </select>
            </label>
            <label class="field">
              <span class="field-label">Weight</span>
              <input type="number" min="0" max="255" value={weight} onInput={(event) => setWeight(event.currentTarget.value)} />
            </label>
            <label class="field">
              <span class="field-label">UDP encap</span>
              <input type="number" min="0" value={udpEncap} onInput={(event) => setUdpEncap(event.currentTarget.value)} />
            </label>
            <label class="field">
              <span class="field-label">IPv4 lookup table</span>
              <select value={ipv4LookupTable} onChange={(event) => setIpv4LookupTable(event.currentTarget.value)}>
                <option value="">Omit</option>
                <option value="default">default (0)</option>
                {tableOptions(false).map((item) => (
                  <option value={item.name} key={`v4-${item.id}`}>{item.name} ({item.table_id})</option>
                ))}
              </select>
            </label>
            <label class="field">
              <span class="field-label">IPv6 lookup table</span>
              <select value={ipv6LookupTable} onChange={(event) => setIpv6LookupTable(event.currentTarget.value)}>
                <option value="">Omit</option>
                <option value="default">default (0)</option>
                {tableOptions(true).map((item) => (
                  <option value={item.name} key={`v6-${item.id}`}>{item.name} ({item.table_id})</option>
                ))}
              </select>
            </label>
            <label class="field">
              <span class="field-label">MPLS lookup table ID</span>
              <input type="number" min="0" value={mplsLookupTable} onInput={(event) => setMplsLookupTable(event.currentTarget.value)} />
            </label>
            <label class="field">
              <span class="field-label">rx-ip4 interface</span>
              <input type="text" value={rxIpv4Interface} onInput={(event) => setRxIpv4Interface(event.currentTarget.value)} />
            </label>
            <label class="field">
              <span class="field-label">rx-ip6 interface</span>
              <input type="text" value={rxIpv6Interface} onInput={(event) => setRxIpv6Interface(event.currentTarget.value)} />
            </label>
            <label class="field field-wide">
              <span class="field-label">Out labels</span>
              <input type="text" placeholder="100 200 300" value={outLabels} onInput={(event) => setOutLabels(event.currentTarget.value)} />
            </label>
            <span class="checkbox-row">
              <input type="checkbox" checked={resolveViaHost} onChange={(event) => setResolveViaHost(event.currentTarget.checked)} />
              <span>resolve-via-host</span>
            </span>
            <span class="checkbox-row">
              <input type="checkbox" checked={resolveViaConnected} onChange={(event) => setResolveViaConnected(event.currentTarget.checked)} />
              <span>resolve-via-connected</span>
            </span>
            <span class="checkbox-row">
              <input type="checkbox" checked={drop} onChange={(event) => setDrop(event.currentTarget.checked)} />
              <span>drop</span>
            </span>
          </div>
        </section>

        {error ? <p class="form-error" role="alert">{error}</p> : null}
        <div class="actions">
          <button class="button ghost" type="button" onClick={onBack} disabled={saving}>Cancel</button>
          <button class="button primary" type="submit" disabled={saving || loading}>
            {saving ? "Saving…" : editing ? "Save route" : "Add route"}
          </button>
        </div>
      </form>
    </div>
  );
}
