import { useEffect, useState } from "preact/hooks";
import { APIError, api } from "../../shared/api";
import { showToast } from "../../shared/toast";
import type { Network, StatusResponse, VNF } from "../types";
import { formatDate } from "./format";
import { VNFDialog } from "./VNFDialog";
import { VNFLeasesDialog } from "./VNFLeasesDialog";
import { VNFSessionsDialog } from "./VNFSessionsDialog";

type DialogState = {
  vnf: VNF | null;
  networkID?: number;
};

export function VNFsTab() {
  const [vnfs, setVnfs] = useState<VNF[]>([]);
  const [networks, setNetworks] = useState<Network[]>([]);
  const [vppReady, setVppReady] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [dialog, setDialog] = useState<DialogState | null>(null);
  const [leasesVNF, setLeasesVNF] = useState<VNF | null>(null);
  const [sessionsVNF, setSessionsVNF] = useState<VNF | null>(null);

  const publishVNFs = (list: VNF[]) => {
    setVnfs(list);
    window.dispatchEvent(new CustomEvent("rookery:vnfs-changed", { detail: list }));
  };

  const loadVNFs = async (showErrors = true) => {
    try {
      const data = await api<VNF[]>("/api/vnfs");
      publishVNFs(Array.isArray(data) ? data : []);
      if (showErrors) {
        setError("");
      }
    } catch (err) {
      if (showErrors) {
        setError(err instanceof Error ? err.message : String(err));
      }
    }
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
      void loadVNFs(false);
    };
    const onVPPReady = (event: Event) => {
      setVppReady(Boolean((event as CustomEvent<boolean>).detail));
    };
    const onVNFData = (event: Event) => {
      const detail = (event as CustomEvent<VNF[]>).detail;
      if (Array.isArray(detail)) {
        setVnfs(detail);
      }
    };
    const onAddVNF = (event: Event) => {
      const networkID = (event as CustomEvent<{ networkId?: number }>).detail?.networkId;
      setDialog({ vnf: null, networkID });
    };
    const onRefreshNetworks = () => {
      void loadNetworks();
    };
    const onTabChanged = (event: Event) => {
      if ((event as CustomEvent<string>).detail !== "vnfs") {
        setDialog(null);
        setLeasesVNF(null);
        setSessionsVNF(null);
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

  const handleSaved = (saved: VNF) => {
    setDialog(null);
    setError("");
    showToast(`${dialog?.vnf ? "Saved" : "Added"} VNF ${saved.name}`);
    void loadVNFs(false);
    if (saved.type === "nat44") {
      refreshNetworksInLegacy();
    }
  };

  const startVNF = async (vnf: VNF) => {
    try {
      await api<VNF>(`/api/vnfs/${vnf.id}/restart`, { method: "POST" });
      setError("");
      showToast(`Started VNF ${vnf.name}`);
      void loadVNFs(false);
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
        ? `Delete VNF "${vnf.name}"?\n\nThis stops NAT44 and removes its VPP rules. The network BVI is kept until the network is deleted.`
        : `Delete VNF "${vnf.name}"?\n\nThis stops the function, removes its tap, and deletes its leases.`
    );
    if (!confirmed) {
      return;
    }

    try {
      await api(`/api/vnfs/${vnf.id}`, { method: "DELETE" });
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

  const enabled = vnfs.filter((vnf) => vnf.enabled).length;
  const leaseCount = vnfs.reduce((sum, vnf) => sum + (vnf.lease_count || 0), 0);
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
          <div class="actions">
            <button class="button ghost" type="button" onClick={() => void refreshAll()}>
              Refresh
            </button>
            <button
              class="button primary"
              type="button"
              disabled={networks.length === 0}
              title={networks.length === 0 ? "Create a Network first" : ""}
              onClick={() => setDialog({ vnf: null })}
            >
              <span class="button-plus" aria-hidden="true">
                +
              </span>
              Add VNF
            </button>
          </div>
        </div>

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
                  const typeLabel =
                    vnf.type === "nat44" ? "NAT44" : vnf.type === "dhcpv6" ? "DHCPv6" : "DHCPv4";
                  const dnatCount = Array.isArray(vnf.config.dnat_rules) ? vnf.config.dnat_rules.length : 0;
                  return (
                    <tr key={vnf.id}>
                      <td>
                        <span class="vm-name">{vnf.name}</span>
                        {vnf.type === "nat44" && vnf.config.outside_interface ? (
                          <span class="vm-description">
                            outside {vnf.config.outside_interface}
                            {dnatCount ? ` · ${dnatCount} DNAT` : ""}
                          </span>
                        ) : vnf.tap_host_name ? (
                          <span class="vm-description">
                            {vnf.tap_host_name}
                            {vnf.type === "dhcpv4" &&
                            Array.isArray(vnf.config.static_assignments) &&
                            vnf.config.static_assignments.length > 0
                              ? ` · ${vnf.config.static_assignments.length} static`
                              : ""}
                          </span>
                        ) : null}
                      </td>
                      <td class="mono">{typeLabel}</td>
                      <td class="mono">{network?.name || vnf.network_name || `network ${vnf.network_id}`}</td>
                      <td>
                        <span class="status-badge" data-status={vnf.status || "disabled"} title={vnf.status_detail}>
                          {vnf.status || "disabled"}
                        </span>
                      </td>
                      <td class={vnf.lease_count && vnf.type !== "nat44" ? "mono" : "mono muted"}>
                        {vnf.type === "nat44" ? "—" : String(vnf.lease_count || 0)}
                      </td>
                      <td class="updated hide-sm">{formatDate(vnf.updated_at)}</td>
                      <td>
                        <div class="actions">
                          <button
                            class="row-action"
                            type="button"
                            onClick={() =>
                              vnf.type === "nat44" ? setSessionsVNF(vnf) : setLeasesVNF(vnf)
                            }
                          >
                            {vnf.type === "nat44" ? "Sessions" : "Leases"}
                          </button>
                          <button class="row-action" type="button" onClick={() => setDialog({ vnf })}>
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
              <p>Add a DHCPv4 or DHCPv6 function and attach it to a Network.</p>
              <button
                class="button primary"
                type="button"
                disabled={networks.length === 0}
                onClick={() => setDialog({ vnf: null })}
              >
                Add a VNF
              </button>
            </div>
          )}
        </div>
      </section>

      {dialog ? (
        <VNFDialog
          key={dialog.vnf ? `edit-${dialog.vnf.id}` : `new-${dialog.networkID ?? "default"}`}
          vnf={dialog.vnf}
          networks={networks}
          vppReady={vppReady}
          initialNetworkID={dialog.networkID}
          onClose={() => setDialog(null)}
          onSaved={handleSaved}
        />
      ) : null}

      {leasesVNF ? (
        <VNFLeasesDialog key={`leases-${leasesVNF.id}`} vnf={leasesVNF} onClose={() => setLeasesVNF(null)} />
      ) : null}

      {sessionsVNF ? (
        <VNFSessionsDialog
          key={`sessions-${sessionsVNF.id}`}
          vnf={sessionsVNF}
          onClose={() => setSessionsVNF(null)}
        />
      ) : null}
    </>
  );
}
