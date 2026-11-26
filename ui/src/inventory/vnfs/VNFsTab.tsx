import { useEffect, useRef, useState } from "preact/hooks";
import { APIError, api } from "../../shared/api";
import { showToast } from "../../shared/toast";
import { EntityLabels, useEntityLabels } from "../../shared/labels";
import type {
  BGPVNF,
  BGPVNFRecord,
  DHCPVNF,
  DHCPVNFRecord,
  DNSVNF,
  DNSVNFRecord,
  NAT44VNF,
  NAT44VNFRecord,
  Network,
  StatusResponse,
  VNF,
  VNFType,
} from "../types";
import {
  formatDate,
  sortVNFs,
  tagBGPVNF,
  tagDHCPVNF,
  tagDNSVNF,
  tagNAT44VNF,
  vnfAPISlug,
  vnfLeaseCount,
  vnfSummary,
  vnfTypeLabel,
} from "./format";
import { VNFPage } from "./VNFPage";
import { VNFBGPStatusDialog } from "./VNFBGPStatusDialog";
import { VNFDNSRecordsDialog } from "./VNFDNSRecordsDialog";
import { VNFLeasesDialog } from "./VNFLeasesDialog";
import { VNFSessionsDialog } from "./VNFSessionsDialog";
import { navigate, useRoute } from "../router";

type DialogState = {
  vnf: VNF | null;
  networkID?: number;
  type?: VNFType;
};

/** ADD_TYPES is the order of the per-type Add VNF buttons. */
const ADD_TYPES: VNFType[] = ["dhcpv4", "dhcpv6", "dns", "nat44", "bgp"];

/** isVNFType narrows an untrusted route/event value to a VNF type. */
function isVNFType(value: string | null | undefined): value is VNFType {
  return (
    value === "dhcpv4" ||
    value === "dhcpv6" ||
    value === "dns" ||
    value === "nat44" ||
    value === "bgp"
  );
}

/**
 * VNFsTab renders the merged VNF inventory table with status, per-type counts, and row actions for
 * monitoring, editing, restarting, and deleting network functions. It fetches the four typed lists
 * (without network_id because the table spans every network) and merges them, shows a partial list
 * with a banner when one type fails, keeps dialogs in sync with the path route, and reacts to
 * rookery:* window events.
 */
export function VNFsTab() {
  const [vnfs, setVnfs] = useState<VNF[]>([]);
  const [networks, setNetworks] = useState<Network[]>([]);
  const [vppReady, setVppReady] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [loadError, setLoadError] = useState("");
  const [dialog, setDialog] = useState<DialogState | null>(null);
  const [leasesVNF, setLeasesVNF] = useState<DHCPVNF | null>(null);
  const [sessionsVNF, setSessionsVNF] = useState<NAT44VNF | null>(null);
  const [bgpVNF, setBGPVNF] = useState<BGPVNF | null>(null);
  const [dnsVNF, setDNSVNF] = useState<DNSVNF | null>(null);
  const vnfsRef = useRef<VNF[]>([]);
  const entityLabels = useEntityLabels("vnf");
  const route = useRoute();

  useEffect(() => {
    if (route.segments[0] !== "vnfs") {
      setDialog(null);
      setLeasesVNF(null);
      setSessionsVNF(null);
      setBGPVNF(null);
      setDNSVNF(null);
      return;
    }
    const action = route.segments[1];
    if (action === "new") {
      const networkID = Number(route.query.get("network")) || undefined;
      const requestedType = route.query.get("type");
      const type = isVNFType(requestedType) ? requestedType : undefined;
      setLeasesVNF(null);
      setSessionsVNF(null);
      setBGPVNF(null);
      setDNSVNF(null);
      setDialog({ vnf: null, networkID, type });
      return;
    }
    if (action && action !== "new") {
      const vnf = vnfs.find((item) => String(item.id) === action);
      const mode = route.segments[2];
      if (!vnf) {
        setDialog(null);
        setLeasesVNF(null);
        setSessionsVNF(null);
        setBGPVNF(null);
        setDNSVNF(null);
        return;
      }
      setDialog(mode === "edit" ? { vnf } : null);
      setLeasesVNF(mode === "leases" && (vnf.type === "dhcpv4" || vnf.type === "dhcpv6") ? vnf : null);
      setSessionsVNF(mode === "sessions" && vnf.type === "nat44" ? vnf : null);
      setBGPVNF(mode === "bgp" && vnf.type === "bgp" ? vnf : null);
      setDNSVNF(mode === "dns" && vnf.type === "dns" ? vnf : null);
      return;
    }
    setDialog(null);
    setLeasesVNF(null);
    setSessionsVNF(null);
    setBGPVNF(null);
    setDNSVNF(null);
  }, [route.path, route.search, vnfs]);

  const publishVNFs = (list: VNF[]) => {
    vnfsRef.current = list;
    setVnfs(list);
    window.dispatchEvent(new CustomEvent("rookery:vnfs-changed", { detail: list }));
  };

  const loadVNFs = async () => {
    const previous = vnfsRef.current;
    const [dhcpResult, dnsResult, nat44Result, bgpResult] = await Promise.allSettled([
      api<DHCPVNFRecord[]>("/api/dhcp-vnfs"),
      api<DNSVNFRecord[]>("/api/dns-vnfs"),
      api<NAT44VNFRecord[]>("/api/nat44-vnfs"),
      api<BGPVNFRecord[]>("/api/bgp-vnfs"),
    ]);
    const failures: string[] = [];
    const pick = <T extends { id: number }>(
      result: PromiseSettledResult<T[]>,
      label: string,
      keep: (candidate: VNF) => boolean,
      tag: (record: T) => VNF
    ): VNF[] => {
      if (result.status === "fulfilled") {
        return (Array.isArray(result.value) ? result.value : []).map(tag);
      }
      failures.push(
        `${label}: ${result.reason instanceof Error ? result.reason.message : String(result.reason)}`
      );
      return previous.filter(keep);
    };
    const merged = sortVNFs([
      ...pick(
        dhcpResult,
        "DHCP",
        (candidate) => candidate.type === "dhcpv4" || candidate.type === "dhcpv6",
        tagDHCPVNF
      ),
      ...pick(dnsResult, "DNS", (candidate) => candidate.type === "dns", tagDNSVNF),
      ...pick(nat44Result, "NAT44", (candidate) => candidate.type === "nat44", tagNAT44VNF),
      ...pick(bgpResult, "BGP", (candidate) => candidate.type === "bgp", tagBGPVNF),
    ]);
    publishVNFs(merged);
    setLoadError(
      failures.length === 0
        ? ""
        : `Some VNF types could not be loaded, so the table is partial — ${failures.join("; ")}`
    );
  };

  const loadNetworks = async () => {
    try {
      const data = await api<Network[]>("/api/networks");
      setNetworks(Array.isArray(data) ? data : []);
    } catch {
      setNetworks([]);
    }
  };

  const loadStatus = async () => {
    try {
      const status = await api<StatusResponse>("/api/status");
      setVppReady(Boolean(status.vpp?.ready));
    } catch {
      setVppReady(false);
    }
  };

  const refreshAll = async () => {
    setLoading(true);
    await Promise.allSettled([loadVNFs(), loadNetworks(), loadStatus()]);
    setLoading(false);
  };

  useEffect(() => {
    void refreshAll();

    const onNetworksChanged = (event: Event) => {
      const detail = (event as CustomEvent<Network[]>).detail;
      if (Array.isArray(detail)) {
        setNetworks(detail);
      } else {
        void loadNetworks();
      }
      void loadVNFs();
    };
    const onVPPReady = (event: Event) => {
      setVppReady(Boolean((event as CustomEvent<boolean>).detail));
    };
    const onVNFData = (event: Event) => {
      const detail = (event as CustomEvent<VNF[]>).detail;
      if (Array.isArray(detail)) {
        vnfsRef.current = detail;
        setVnfs(detail);
      }
    };
    const onAddVNF = (event: Event) => {
      const detail = (event as CustomEvent<{ networkId?: number; type?: string }>).detail;
      const networkID = detail?.networkId;
      const params = new URLSearchParams();
      if (isVNFType(detail?.type)) {
        params.set("type", detail.type);
      }
      if (networkID) {
        params.set("network", String(networkID));
      }
      const query = params.toString();
      navigate(query ? `/vnfs/new?${query}` : "/vnfs/new");
    };
    const onRefreshNetworks = () => {
      void loadNetworks();
    };
    const onTabChanged = (event: Event) => {
      if ((event as CustomEvent<string>).detail !== "vnfs") {
        setDialog(null);
        setLeasesVNF(null);
        setSessionsVNF(null);
        setBGPVNF(null);
        setDNSVNF(null);
      }
    };

    window.addEventListener("rookery:networks-changed", onNetworksChanged);
    window.addEventListener("rookery:vpp-ready", onVPPReady);
    window.addEventListener("rookery:vnfs-data", onVNFData);
    window.addEventListener("rookery:add-vnf", onAddVNF);
    window.addEventListener("rookery:refresh-networks", onRefreshNetworks);
    window.addEventListener("rookery:tab-changed", onTabChanged);

    return () => {
      window.removeEventListener("rookery:networks-changed", onNetworksChanged);
      window.removeEventListener("rookery:vpp-ready", onVPPReady);
      window.removeEventListener("rookery:vnfs-data", onVNFData);
      window.removeEventListener("rookery:add-vnf", onAddVNF);
      window.removeEventListener("rookery:refresh-networks", onRefreshNetworks);
      window.removeEventListener("rookery:tab-changed", onTabChanged);
    };
    // The listeners use only state setters and stable API helpers.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    const count = document.getElementById("vnf-tab-count");
    if (count) {
      count.textContent = String(vnfs.length);
    }
  }, [vnfs.length]);

  const refreshNetworksInLegacy = () => {
    window.dispatchEvent(new CustomEvent("rookery:refresh-networks"));
  };

  /** openAdd opens the typed add page, optionally scoped to a network. */
  const openAdd = (type: VNFType, networkID?: number) => {
    const params = new URLSearchParams({ type });
    if (networkID) {
      params.set("network", String(networkID));
    }
    navigate(`/vnfs/new?${params.toString()}`);
  };

  const handleSaved = (saved: VNF) => {
    setDialog(null);
    navigate("/vnfs", true);
    setError("");
    showToast(`${dialog?.vnf ? "Saved" : "Added"} VNF ${saved.name}`);
    void loadVNFs();
    if (saved.type === "nat44" || saved.type === "dns") {
      refreshNetworksInLegacy();
    }
  };

  const startVNF = async (vnf: VNF) => {
    try {
      await api(`/api/${vnfAPISlug(vnf.type)}/${vnf.id}/restart`, { method: "POST" });
      setError("");
      showToast(`Started VNF ${vnf.name}`);
      void loadVNFs();
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      setError(`Could not start ${vnf.name}: ${message}`);
      if (err instanceof APIError && err.status === 503) {
        void loadStatus();
      }
    }
  };

  const deleteVNF = async (vnf: VNF) => {
    const confirmed = window.confirm(
      vnf.type === "nat44"
        ? `Delete VNF "${vnf.name}"?\n\nThis stops NAT44 and removes its VPP rules and DNAT mappings. The network BVI is kept until the network is deleted.`
        : vnf.type === "dns"
          ? `Delete VNF "${vnf.name}"?\n\nThis stops the DNS server and removes its local records and forwarders.`
          : vnf.type === "bgp"
            ? `Delete VNF "${vnf.name}"?\n\nThis stops the BGP speaker and removes its advertised prefixes.`
            : `Delete VNF "${vnf.name}"?\n\nThis stops the function, removes its tap, and deletes its leases, static assignments, and options.`
    );
    if (!confirmed) {
      return;
    }

    try {
      await api(`/api/${vnfAPISlug(vnf.type)}/${vnf.id}`, { method: "DELETE" });
      const next = vnfs.filter((item) => item.id !== vnf.id);
      publishVNFs(next);
      setError("");
      showToast(`Deleted VNF ${vnf.name}`);
      if (vnf.type === "nat44") {
        refreshNetworksInLegacy();
      }
    } catch (err) {
      setError(`Could not delete ${vnf.name}: ${err instanceof Error ? err.message : String(err)}`);
    }
  };

  if (dialog) {
    return (
      <VNFPage
        key={
          dialog.vnf
            ? `edit-${dialog.vnf.id}`
            : `new-${dialog.type ?? "any"}-${dialog.networkID ?? "default"}`
        }
        vnf={dialog.vnf}
        networks={networks}
        vppReady={vppReady}
        initialNetworkID={dialog.networkID}
        initialType={dialog.type}
        onClose={() => {
          setDialog(null);
          navigate("/vnfs", true);
        }}
        onSaved={handleSaved}
      />
    );
  }

  const enabled = vnfs.filter((vnf) => vnf.enabled).length;
  const leaseCount = vnfs.reduce((sum, vnf) => sum + (vnfLeaseCount(vnf) ?? 0), 0);
  const summary =
    vnfs.length === 0
      ? loading
        ? "Loading VNFs…"
        : "No network functions registered"
      : `${vnfs.length} VNF${vnfs.length === 1 ? "" : "s"} · ${enabled} enabled · ${leaseCount} active leases`;

  return (
    <>
      <section class="panel" aria-labelledby="vnfs-heading">
        <div class="panel-head">
          <div>
            <h2 id="vnfs-heading">Network functions</h2>
            <p class="panel-subtitle">{summary}</p>
          </div>
          <div class="actions vnf-add-actions">
            <button class="button ghost" type="button" onClick={() => void refreshAll()}>
              Refresh
            </button>
            {ADD_TYPES.map((type) => (
              <button
                class="button primary"
                type="button"
                key={type}
                disabled={networks.length === 0}
                aria-label={`Add a ${vnfTypeLabel(type)} VNF`}
                title={
                  networks.length === 0
                    ? "Create a Network first"
                    : `Add a ${vnfTypeLabel(type)} VNF`
                }
                onClick={() => openAdd(type)}
              >
                <span class="button-plus" aria-hidden="true">
                  +
                </span>
                {vnfTypeLabel(type)}
              </button>
            ))}
          </div>
        </div>

        {loadError ? (
          <div class="banner" role="alert">
            {loadError}
          </div>
        ) : null}
        {error ? (
          <div class="banner" role="alert">
            {error}
          </div>
        ) : null}

        <div class="table-wrap">
          {vnfs.length > 0 ? (
            <table>
              <thead>
                <tr>
                  <th scope="col">Name</th>
                  <th scope="col">Type</th>
                  <th scope="col">Network</th>
                  <th scope="col">Status</th>
                  <th scope="col">Active leases</th>
                  <th scope="col" class="hide-sm">
                    Updated
                  </th>
                  <th scope="col">
                    <span class="sr-only">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {vnfs.map((vnf) => {
                  const network = networks.find((item) => item.id === vnf.network_id);
                  const leases = vnfLeaseCount(vnf);
                  return (
                    <tr key={vnf.id}>
                      <td>
                        <span class="vm-name">{vnf.name}</span>
                        <span class="vm-description">{vnfSummary(vnf)}</span>
                        <EntityLabels
                          entityType="vnf"
                          entityID={vnf.id}
                          name={vnf.name}
                          labels={entityLabels.labels[String(vnf.id)]}
                          onSaved={(labels) => entityLabels.setEntityLabels(String(vnf.id), labels)}
                        />
                      </td>
                      <td class="mono">{vnfTypeLabel(vnf.type)}</td>
                      <td class="mono">{network?.name || vnf.network_name || `network ${vnf.network_id}`}</td>
                      <td>
                        <span class="status-badge" data-status={vnf.status || "disabled"} title={vnf.status_detail}>
                          {vnf.status || "disabled"}
                        </span>
                      </td>
                      <td class={leases ? "mono" : "mono muted"}>
                        {leases === undefined ? "—" : String(leases)}
                      </td>
                      <td class="updated hide-sm">{formatDate(vnf.updated_at)}</td>
                      <td>
                        <div class="actions">
                          {vnf.type === "bgp" ? (
                            <button
                              class="row-action"
                              type="button"
                              onClick={() => navigate(`/vnfs/${vnf.id}/bgp`)}
                            >
                              Status
                            </button>
                          ) : vnf.type === "dns" ? (
                            <button
                              class="row-action"
                              type="button"
                              onClick={() => navigate(`/vnfs/${vnf.id}/dns`)}
                            >
                              Records
                            </button>
                          ) : vnf.type === "nat44" ? (
                            <button
                              class="row-action"
                              type="button"
                              onClick={() => navigate(`/vnfs/${vnf.id}/sessions`)}
                            >
                              Sessions
                            </button>
                          ) : (
                            <button
                              class="row-action"
                              type="button"
                              onClick={() => navigate(`/vnfs/${vnf.id}/leases`)}
                            >
                              Leases
                            </button>
                          )}
                          <button class="row-action" type="button" onClick={() => navigate(`/vnfs/${vnf.id}/edit`)}>
                            Edit
                          </button>
                          <button
                            class="row-action"
                            type="button"
                            disabled={!vppReady}
                            title={vppReady ? "" : "VPP must be ready to start a VNF."}
                            onClick={() => void startVNF(vnf)}
                          >
                            {vnf.enabled ? "Restart" : "Start"}
                          </button>
                          <button class="row-action danger" type="button" onClick={() => void deleteVNF(vnf)}>
                            Delete
                          </button>
                        </div>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          ) : (
            <div class="empty">
              <div class="empty-mark" aria-hidden="true">
                ◈
              </div>
              <h3>No network functions yet</h3>
              <p>Add a DHCP, DNS, NAT44, or BGP function and attach it to a Network.</p>
              <div class="actions vnf-add-actions">
                {ADD_TYPES.map((type) => (
                  <button
                    class="button primary"
                    type="button"
                    key={type}
                    disabled={networks.length === 0}
                    onClick={() => openAdd(type)}
                  >
                    Add {vnfTypeLabel(type)}
                  </button>
                ))}
              </div>
            </div>
          )}
        </div>
      </section>

      {leasesVNF ? (
        <VNFLeasesDialog
          key={`leases-${leasesVNF.id}`}
          vnf={leasesVNF}
          onClose={() => {
            setLeasesVNF(null);
            navigate("/vnfs", true);
          }}
        />
      ) : null}

      {sessionsVNF ? (
        <VNFSessionsDialog
          key={`sessions-${sessionsVNF.id}`}
          vnf={sessionsVNF}
          onClose={() => {
            setSessionsVNF(null);
            navigate("/vnfs", true);
          }}
        />
      ) : null}

      {bgpVNF ? (
        <VNFBGPStatusDialog
          key={`bgp-${bgpVNF.id}`}
          vnf={bgpVNF}
          onClose={() => {
            setBGPVNF(null);
            navigate("/vnfs", true);
          }}
        />
      ) : null}

      {dnsVNF ? (
        <VNFDNSRecordsDialog
          key={`dns-${dnsVNF.id}`}
          vnf={dnsVNF}
          onClose={() => {
            setDNSVNF(null);
            navigate("/vnfs", true);
          }}
        />
      ) : null}
    </>
  );
}
