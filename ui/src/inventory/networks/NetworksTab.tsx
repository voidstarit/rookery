import { useEffect, useState } from "preact/hooks";
import { api } from "../../shared/api";
import { showToast } from "../../shared/toast";
import type { ACL, Network, StatusResponse, VirtualInterface, VM, VNF } from "../types";
import { ACLDialog } from "./ACLDialog";
import { AttachDialog } from "./AttachDialog";
import { InterfaceACLDialog } from "./InterfaceACLDialog";
import { InterfaceDialog } from "./InterfaceDialog";
import { NetworkDialog } from "./NetworkDialog";
import { aclAssignments } from "./acl";

function formatDate(value: string | undefined): string {
  if (!value) {
    return "—";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "—";
  }
  return date.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}

type BackendData = {
  networks: Network[];
  interfaces: VirtualInterface[];
  acls: ACL[];
  vms: VM[];
  vnfs: VNF[];
};

export function NetworksTab() {
  const [networks, setNetworks] = useState<Network[]>([]);
  const [interfaces, setInterfaces] = useState<VirtualInterface[]>([]);
  const [acls, setAcls] = useState<ACL[]>([]);
  const [vms, setVms] = useState<VM[]>([]);
  const [vnfs, setVnfs] = useState<VNF[]>([]);
  const [vppReady, setVppReady] = useState(false);
  const [loading, setLoading] = useState(true);
  const [networkError, setNetworkError] = useState("");
  const [interfaceError, setInterfaceError] = useState("");
  const [aclError, setAclError] = useState("");
  const [networkDialogOpen, setNetworkDialogOpen] = useState(false);
  const [interfaceDialogOpen, setInterfaceDialogOpen] = useState(false);
  const [attachIface, setAttachIface] = useState<VirtualInterface | null>(null);
  const [aclDialog, setAclDialog] = useState<{ acl: ACL | null } | null>(null);
  const [interfaceACLIface, setInterfaceACLIface] = useState<VirtualInterface | null>(null);

  const setData = (partial: Partial<BackendData>) => {
    if (partial.networks) {
      setNetworks(partial.networks);
      window.dispatchEvent(
        new CustomEvent("rookery:networks-changed", { detail: partial.networks })
      );
    }
    if (partial.interfaces) {
      setInterfaces(partial.interfaces);
      window.dispatchEvent(
        new CustomEvent("rookery:interfaces-changed", { detail: partial.interfaces })
      );
    }
    if (partial.acls) {
      setAcls(partial.acls);
    }
    if (partial.vms) {
      setVms(partial.vms);
    }
    if (partial.vnfs) {
      setVnfs(partial.vnfs);
    }
  };

  const loadNetworks = async (showErrors = true) => {
    try {
      const data = await api<Network[]>("/api/networks");
      const list = Array.isArray(data) ? data : [];
      list.sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: "base" }));
      setData({ networks: list });
      if (showErrors) {
        setNetworkError("");
      }
    } catch (err) {
      if (showErrors) {
        setNetworkError(err instanceof Error ? err.message : String(err));
      }
    }
  };

  const loadInterfaces = async (showErrors = true) => {
    try {
      const data = await api<VirtualInterface[]>("/api/interfaces");
      const list = Array.isArray(data) ? data : [];
      list.sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: "base" }));
      setData({ interfaces: list });
      if (showErrors) {
        setInterfaceError("");
      }
    } catch (err) {
      if (showErrors) {
        setInterfaceError(err instanceof Error ? err.message : String(err));
      }
    }
  };

  const loadACLs = async (showErrors = true) => {
    try {
      const data = await api<ACL[]>("/api/acls");
      const list = Array.isArray(data) ? data : [];
      list.sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: "base" }));
      setData({ acls: list });
      if (showErrors) {
        setAclError("");
      }
    } catch (err) {
      if (showErrors) {
        setAclError(err instanceof Error ? err.message : String(err));
      }
    }
  };

  const loadVMs = async () => {
    try {
      const data = await api<VM[]>("/api/vms");
      setData({ vms: Array.isArray(data) ? data : [] });
    } catch {
      setData({ vms: [] });
    }
  };

  const loadVNFs = async () => {
    try {
      const data = await api<VNF[]>("/api/vnfs");
      setData({ vnfs: Array.isArray(data) ? data : [] });
    } catch {
      setData({ vnfs: [] });
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
    await Promise.allSettled([
      loadNetworks(),
      loadInterfaces(),
      loadACLs(),
      loadVMs(),
      loadVNFs(),
      loadStatus(),
    ]);
    setLoading(false);
  };

  useEffect(() => {
    void refreshAll();

    const onVPPReady = (event: Event) => {
      setVppReady(Boolean((event as CustomEvent<boolean>).detail));
    };
    const onAddNetwork = () => {
      setNetworkDialogOpen(true);
    };
    const onInterfaceData = (event: Event) => {
      const detail = (event as CustomEvent<VirtualInterface[]>).detail;
      if (Array.isArray(detail)) {
        setInterfaces(detail);
      }
    };
    const onVNFData = (event: Event) => {
      const detail = (event as CustomEvent<VNF[]>).detail;
      if (Array.isArray(detail)) {
        setVnfs(detail);
      }
    };
    const onTabChanged = (event: Event) => {
      if ((event as CustomEvent<string>).detail === "networks") {
        void refreshAll();
      }
    };
    const onRefreshNetworks = () => {
      void loadNetworks(false);
    };

    window.addEventListener("rookery:vpp-ready", onVPPReady);
    window.addEventListener("rookery:add-network", onAddNetwork);
    window.addEventListener("rookery:interfaces-data", onInterfaceData);
    window.addEventListener("rookery:vnfs-data", onVNFData);
    window.addEventListener("rookery:vnfs-changed", onVNFData);
    window.addEventListener("rookery:tab-changed", onTabChanged);
    window.addEventListener("rookery:refresh-networks", onRefreshNetworks);

    return () => {
      window.removeEventListener("rookery:vpp-ready", onVPPReady);
      window.removeEventListener("rookery:add-network", onAddNetwork);
      window.removeEventListener("rookery:interfaces-data", onInterfaceData);
      window.removeEventListener("rookery:vnfs-data", onVNFData);
      window.removeEventListener("rookery:vnfs-changed", onVNFData);
      window.removeEventListener("rookery:tab-changed", onTabChanged);
      window.removeEventListener("rookery:refresh-networks", onRefreshNetworks);
    };
    // The listeners use only state setters and stable API helpers.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    const count = document.getElementById("network-tab-count");
    if (count) {
      count.textContent = String(networks.length);
    }
  }, [networks.length]);

  useEffect(() => {
    const newNetwork = document.getElementById("new-network") as HTMLButtonElement | null;
    if (!newNetwork) {
      return;
    }
    newNetwork.disabled = !vppReady;
    newNetwork.title = vppReady ? "" : "Network creation is blocked until FD.io VPP is ready.";
  }, [vppReady]);

  const deleteNetwork = async (network: Network) => {
    const count = interfaces.filter((iface) => iface.network_id === network.id).length;
    if (count > 0) {
      showToast("Delete the virtual interfaces in this network first.", true);
      return;
    }
    const networkVNFs = vnfs.filter((vnf) => vnf.network_id === network.id);
    const confirmed = window.confirm(
      `Delete Rookery Network "${network.name}"?\n\nThis removes the VPP bridge domain and ${networkVNFs.length} VNF(s).`
    );
    if (!confirmed) {
      return;
    }
    try {
      await api(`/api/networks/${network.id}`, { method: "DELETE" });
      const nextNetworks = networks.filter((item) => item.id !== network.id);
      setData({ networks: nextNetworks });
      setNetworkError("");
      showToast(`Deleted network ${network.name}`);
      void loadVNFs();
    } catch (err) {
      setNetworkError(
        `Could not delete ${network.name}: ${err instanceof Error ? err.message : String(err)}`
      );
    }
  };

  const deleteInterface = async (iface: VirtualInterface) => {
    if (iface.attached_vm_id) {
      showToast("Detach the interface from its VM first.", true);
      return;
    }
    const confirmed = window.confirm(
      `Delete virtual interface "${iface.name}"?\n\nThis removes the VPP vhost-user interface and socket.`
    );
    if (!confirmed) {
      return;
    }
    try {
      await api(`/api/interfaces/${iface.id}`, { method: "DELETE" });
      setData({ interfaces: interfaces.filter((item) => item.id !== iface.id) });
      setInterfaceError("");
      showToast(`Deleted interface ${iface.name}`);
    } catch (err) {
      setInterfaceError(
        `Could not delete ${iface.name}: ${err instanceof Error ? err.message : String(err)}`
      );
    }
  };

  const detachInterface = async (iface: VirtualInterface) => {
    const owner = iface.attached_vm_id
      ? vms.find((vm) => vm.id === iface.attached_vm_id)
      : null;
    if (owner && (owner.status === "running" || owner.status === "starting")) {
      showToast("Stop the VM before detaching its interface.", true);
      return;
    }
    if (!window.confirm(`Detach interface "${iface.name}" from its VM?`)) {
      return;
    }
    try {
      const updated = await api<VirtualInterface>(`/api/interfaces/${iface.id}`, {
        method: "PUT",
        body: JSON.stringify({ attached_vm_id: null }),
      });
      setData({ interfaces: interfaces.map((item) => (item.id === updated.id ? updated : item)) });
      setInterfaceError("");
      showToast(`Detached ${iface.name}`);
    } catch (err) {
      setInterfaceError(
        `Could not detach ${iface.name}: ${err instanceof Error ? err.message : String(err)}`
      );
    }
  };

  const deleteACL = async (acl: ACL) => {
    if (aclAssignments(acl, interfaces).length > 0) {
      showToast(`Remove ACL ${acl.name} from its interfaces first.`, true);
      return;
    }
    if (!window.confirm(`Delete ACL "${acl.name}"?`)) {
      return;
    }
    try {
      await api(`/api/acls/${acl.id}`, { method: "DELETE" });
      setData({ acls: acls.filter((item) => item.id !== acl.id) });
      setAclError("");
      showToast(`Deleted ACL ${acl.name}`);
    } catch (err) {
      setAclError(`Could not delete ${acl.name}: ${err instanceof Error ? err.message : String(err)}`);
      showToast(`Could not delete ACL ${acl.name}: ${err instanceof Error ? err.message : String(err)}`, true);
    }
  };

  const addVNF = (network: Network) => {
    window.dispatchEvent(new CustomEvent("rookery:open-tab", { detail: "vnfs" }));
    window.dispatchEvent(new CustomEvent("rookery:add-vnf", { detail: { networkId: network.id } }));
  };

  const attachedCount = interfaces.filter((iface) => iface.attached_vm_id).length;
  const assignedACLs = acls.filter((acl) => aclAssignments(acl, interfaces).length > 0).length;
  const networkSummary =
    networks.length === 0
      ? loading
        ? "Loading networks…"
        : "No Rookery Networks registered"
      : `${networks.length} network${networks.length === 1 ? "" : "s"} · ${interfaces.length} interface${interfaces.length === 1 ? "" : "s"} · ${vnfs.length} VNF${vnfs.length === 1 ? "" : "s"}`;
  const interfaceSummary =
    interfaces.length === 0
      ? loading
        ? "Loading interfaces…"
        : "No virtual interfaces registered"
      : `${interfaces.length} interface${interfaces.length === 1 ? "" : "s"} · ${attachedCount} attached`;
  const aclSummary =
    acls.length === 0
      ? loading
        ? "Loading ACLs…"
        : "No ACLs defined"
      : `${acls.length} ACL${acls.length === 1 ? "" : "s"} · ${assignedACLs} assigned`;

  return (
    <>
      <section class="panel" aria-labelledby="networks-heading">
        <div class="panel-head">
          <div>
            <h2 id="networks-heading">Network</h2>
            <p class="panel-subtitle">{networkSummary}</p>
          </div>
          <button class="button ghost" type="button" disabled={loading} onClick={() => void refreshAll()}>
            {loading ? "Refreshing…" : "Refresh"}
          </button>
        </div>

        {networkError ? (
          <div class="banner" role="alert">
            {networkError}
          </div>
        ) : null}

        <div class="table-wrap">
          {networks.length > 0 ? (
            <table>
              <thead>
                <tr>
                  <th scope="col">Name</th>
                  <th scope="col">Bridge domain</th>
                  <th scope="col">Interfaces</th>
                  <th scope="col">VNFs</th>
                  <th scope="col" class="hide-sm">
                    Updated
                  </th>
                  <th scope="col">
                    <span class="sr-only">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {networks.map((network) => {
                  const count = interfaces.filter((iface) => iface.network_id === network.id).length;
                  const networkVNFs = vnfs.filter((vnf) => vnf.network_id === network.id);
                  return (
                    <tr key={network.id}>
                      <td>
                        <span class="vm-name">{network.name}</span>
                        {network.bvi_host_name ? (
                          <span class="vm-description">
                            {network.bvi_host_name}
                            {network.ipv4_gateway ? ` · ${network.ipv4_gateway}` : ""}
                          </span>
                        ) : null}
                      </td>
                      <td class="mono">bd {network.bridge_domain_id}</td>
                      <td class={count ? "mono" : "mono muted"}>{String(count)}</td>
                      <td class={networkVNFs.length ? "mono" : "mono muted"}>
                        {networkVNFs.length
                          ? networkVNFs.map((vnf) => `${vnf.name} (${vnf.type})`).join(", ")
                          : "none"}
                      </td>
                      <td class="updated hide-sm">{formatDate(network.updated_at)}</td>
                      <td>
                        <div class="actions">
                          <button class="row-action" type="button" onClick={() => addVNF(network)}>
                            Add VNF
                          </button>
                          <button
                            class="row-action danger"
                            type="button"
                            disabled={count > 0 || !vppReady}
                            title={
                              count > 0
                                ? "Delete the virtual interfaces first."
                                : vppReady
                                  ? ""
                                  : "VPP must be ready to delete a bridge domain."
                            }
                            onClick={() => void deleteNetwork(network)}
                          >
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
                ⬢
              </div>
              <h3>No Networks yet</h3>
              <p>Create a VPP bridge domain to connect virtual interfaces and VMs.</p>
              <button
                class="button primary"
                type="button"
                disabled={!vppReady}
                onClick={() => setNetworkDialogOpen(true)}
              >
                Create a network
              </button>
            </div>
          )}
        </div>
      </section>

      <section class="panel panel-spaced" aria-labelledby="interfaces-heading">
        <div class="panel-head">
          <div>
            <h2 id="interfaces-heading">Rookery Virtual Interfaces</h2>
            <p class="panel-subtitle">{interfaceSummary}</p>
          </div>
          <button
            class="button primary"
            type="button"
            disabled={networks.length === 0 || !vppReady}
            title={
              networks.length === 0
                ? "Create a Rookery Network first"
                : vppReady
                  ? ""
                  : "Interface creation is blocked until FD.io VPP is ready"
            }
            onClick={() => setInterfaceDialogOpen(true)}
          >
            <span class="button-plus" aria-hidden="true">
              +
            </span>
            Add interface
          </button>
        </div>

        {interfaceError ? (
          <div class="banner" role="alert">
            {interfaceError}
          </div>
        ) : null}

        <div class="table-wrap">
          {interfaces.length > 0 ? (
            <table>
              <thead>
                <tr>
                  <th scope="col">Name</th>
                  <th scope="col">Network</th>
                  <th scope="col">MAC</th>
                  <th scope="col">Attached VM</th>
                  <th scope="col">ACLs</th>
                  <th scope="col" class="hide-sm">
                    Updated
                  </th>
                  <th scope="col">
                    <span class="sr-only">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {interfaces.map((iface) => {
                  const owner = iface.attached_vm_id
                    ? vms.find((vm) => vm.id === iface.attached_vm_id)
                    : null;
                  const attached = Boolean(iface.attached_vm_id);
                  const ownerRunning = Boolean(
                    owner && (owner.status === "running" || owner.status === "starting")
                  );
                  const aclParts = [];
                  if (iface.ingress_acl_name) {
                    aclParts.push(`from: ${iface.ingress_acl_name}`);
                  }
                  if (iface.egress_acl_name) {
                    aclParts.push(`to: ${iface.egress_acl_name}`);
                  }
                  return (
                    <tr key={iface.id}>
                      <td>
                        <span class="vm-name">{iface.name}</span>
                        {iface.socket_path ? (
                          <span class="vm-description" title={iface.socket_path}>
                            {iface.socket_path}
                          </span>
                        ) : null}
                      </td>
                      <td class="mono">{iface.network_name || "—"}</td>
                      <td class="mono">{iface.mac || "—"}</td>
                      <td class={attached ? "mono" : "mono muted"}>
                        {owner ? owner.name : iface.attached_vm_name || "unattached"}
                      </td>
                      <td class={aclParts.length ? "mono" : "mono muted"}>
                        {aclParts.length ? aclParts.join(" · ") : "none"}
                      </td>
                      <td class="updated hide-sm">{formatDate(iface.updated_at)}</td>
                      <td>
                        <div class="actions">
                          <button class="row-action" type="button" onClick={() => setAttachIface(iface)}>
                            Attach
                          </button>
                          {attached ? (
                            <button
                              class="row-action"
                              type="button"
                              disabled={ownerRunning}
                              title={ownerRunning ? "Stop the VM before detaching its interface." : ""}
                              onClick={() => void detachInterface(iface)}
                            >
                              Detach
                            </button>
                          ) : null}
                          <button
                            class="row-action"
                            type="button"
                            disabled={!vppReady}
                            title={vppReady ? "" : "VPP must be ready to apply interface ACLs."}
                            onClick={() => setInterfaceACLIface(iface)}
                          >
                            ACLs
                          </button>
                          <button
                            class="row-action danger"
                            type="button"
                            disabled={attached || !vppReady}
                            title={
                              attached
                                ? "Detach the interface from its VM first."
                                : vppReady
                                  ? ""
                                  : "VPP must be ready to delete a vhost-user interface."
                            }
                            onClick={() => void deleteInterface(iface)}
                          >
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
                ⇄
              </div>
              <h3>No virtual interfaces yet</h3>
              <p>Add a VPP vhost-user interface to a Rookery Network, then attach it to a VM.</p>
            </div>
          )}
        </div>
      </section>

      <section class="panel panel-spaced" aria-labelledby="acls-heading">
        <div class="panel-head">
          <div>
            <h2 id="acls-heading">Network ACLs</h2>
            <p class="panel-subtitle">{aclSummary}</p>
          </div>
          <div class="actions">
            <button class="button ghost" type="button" disabled={loading} onClick={() => void refreshAll()}>
              {loading ? "Refreshing…" : "Refresh"}
            </button>
            <button
              class="button primary"
              type="button"
              disabled={!vppReady}
              title={vppReady ? "" : "ACL changes are blocked until FD.io VPP is ready."}
              onClick={() => setAclDialog({ acl: null })}
            >
              <span class="button-plus" aria-hidden="true">
                +
              </span>
              Add ACL
            </button>
          </div>
        </div>

        {aclError ? (
          <div class="banner" role="alert">
            {aclError}
          </div>
        ) : null}

        <div class="table-wrap">
          {acls.length > 0 ? (
            <table>
              <thead>
                <tr>
                  <th scope="col">Name</th>
                  <th scope="col">Title</th>
                  <th scope="col">Rules</th>
                  <th scope="col">Assigned to</th>
                  <th scope="col" class="hide-sm">
                    Updated
                  </th>
                  <th scope="col">
                    <span class="sr-only">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {acls.map((acl) => {
                  const assignments = aclAssignments(acl, interfaces);
                  return (
                    <tr key={acl.id}>
                      <td>
                        <span class="vm-name">{acl.name}</span>
                        {acl.description ? (
                          <span class="vm-description" title={acl.description}>
                            {acl.description}
                          </span>
                        ) : null}
                      </td>
                      <td>{acl.title || "—"}</td>
                      <td class="mono">
                        {acl.rules?.length || 0} rule{acl.rules?.length === 1 ? "" : "s"}
                      </td>
                      <td class={assignments.length ? "mono" : "mono muted"}>
                        {assignments.length ? assignments.map((iface) => iface.name).join(", ") : "none"}
                      </td>
                      <td class="updated hide-sm">{formatDate(acl.updated_at)}</td>
                      <td>
                        <div class="actions">
                          <button
                            class="row-action"
                            type="button"
                            disabled={!vppReady}
                            title={vppReady ? "" : "VPP must be ready to edit ACLs."}
                            onClick={() => setAclDialog({ acl })}
                          >
                            Edit / Preview
                          </button>
                          <button
                            class="row-action danger"
                            type="button"
                            disabled={assignments.length > 0}
                            title={assignments.length > 0 ? "Remove this ACL from its interfaces first." : ""}
                            onClick={() => void deleteACL(acl)}
                          >
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
                ▤
              </div>
              <h3>No ACLs yet</h3>
              <p>Define ordered ingress/egress rules, then assign an ACL to a virtual interface.</p>
              <button
                class="button primary"
                type="button"
                disabled={!vppReady}
                onClick={() => setAclDialog({ acl: null })}
              >
                Add an ACL
              </button>
            </div>
          )}
        </div>
      </section>

      {networkDialogOpen ? (
        <NetworkDialog
          onClose={() => setNetworkDialogOpen(false)}
          onSaved={(network) => {
            setNetworkDialogOpen(false);
            setData({ networks: [...networks, network].sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: "base" })) });
            setNetworkError("");
            showToast(`Created network ${network.name}`);
          }}
        />
      ) : null}

      {interfaceDialogOpen ? (
        <InterfaceDialog
          networks={networks}
          vms={vms}
          onClose={() => setInterfaceDialogOpen(false)}
          onSaved={(iface) => {
            setInterfaceDialogOpen(false);
            setData({ interfaces: [...interfaces, iface].sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: "base" })) });
            setInterfaceError("");
            showToast(`Created interface ${iface.name}`);
            void loadVMs();
          }}
        />
      ) : null}

      {attachIface ? (
        <AttachDialog
          key={`attach-${attachIface.id}`}
          iface={attachIface}
          vms={vms}
          onClose={() => setAttachIface(null)}
          onSaved={(updated) => {
            setAttachIface(null);
            setData({ interfaces: interfaces.map((item) => (item.id === updated.id ? updated : item)) });
            setInterfaceError("");
            showToast(
              updated.attached_vm_id
                ? `Attached ${updated.name} to ${updated.attached_vm_name || "VM"}`
                : `Detached ${updated.name}`
            );
            void loadVMs();
          }}
        />
      ) : null}

      {aclDialog ? (
        <ACLDialog
          key={aclDialog.acl ? `edit-${aclDialog.acl.id}` : "new"}
          acl={aclDialog.acl}
          vppReady={vppReady}
          onClose={() => setAclDialog(null)}
          onSaved={(saved) => {
            setAclDialog(null);
            setData({
              acls: aclDialog.acl
                ? acls.map((item) => (item.id === saved.id ? saved : item))
                : [...acls, saved].sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: "base" })),
            });
            setAclError("");
            showToast(`${aclDialog.acl ? "Saved" : "Added"} ACL ${saved.name}`);
            void loadInterfaces(false);
          }}
        />
      ) : null}

      {interfaceACLIface ? (
        <InterfaceACLDialog
          key={`acl-${interfaceACLIface.id}`}
          iface={interfaceACLIface}
          acls={acls}
          vppReady={vppReady}
          onClose={() => setInterfaceACLIface(null)}
          onSaved={(updated) => {
            setInterfaceACLIface(null);
            setData({ interfaces: interfaces.map((item) => (item.id === updated.id ? updated : item)) });
            setInterfaceError("");
            showToast(`Applied ACLs to ${updated.name}`);
          }}
        />
      ) : null}
    </>
  );
}
