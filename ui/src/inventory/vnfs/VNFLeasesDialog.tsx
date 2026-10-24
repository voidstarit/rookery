import { useEffect, useRef, useState } from "preact/hooks";
import { api } from "../../shared/api";
import type { VNF, VNFLease } from "../types";
import { formatDate } from "./format";

type VNFLeasesDialogProps = {
  vnf: VNF;
  onClose: () => void;
};

type LeaseResponse = {
  vnf_id: number;
  type: string;
  leases: VNFLease[];
};

export function VNFLeasesDialog({ vnf, onClose }: VNFLeasesDialogProps) {
  const [leases, setLeases] = useState<VNFLease[]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const dialogRef = useRef<HTMLDialogElement | null>(null);

  const load = async () => {
    setLoading(true);
    try {
      const data = await api<LeaseResponse>(`/api/vnfs/${vnf.id}/leases`);
      setLeases(Array.isArray(data.leases) ? data.leases : []);
      setError("");
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    dialogRef.current?.showModal();
    void load();
    // The dialog is remounted for each VNF.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <dialog
      ref={dialogRef}
      class="vnf-leases-dialog"
      aria-labelledby="vnf-leases-title"
      onClose={onClose}
      onClick={(event) => {
        if (event.target === event.currentTarget) {
          dialogRef.current?.close();
        }
      }}
    >
      <header class="dialog-head">
        <div>
          <p class="eyebrow">Lease monitor</p>
          <h2 id="vnf-leases-title">Leases · {vnf.name}</h2>
        </div>
        <button class="icon-button" type="button" aria-label="Close dialog" onClick={() => dialogRef.current?.close()}>
          ×
        </button>
      </header>

      <p class="dialog-lede">
        {vnf.type === "dhcpv6" ? "DHCPv6" : "DHCPv4"} · {vnf.network_name || "network"}
      </p>
      {error ? (
        <div class="banner" role="alert">
          {error}
        </div>
      ) : null}

      <div class="table-wrap">
        {leases.length > 0 ? (
          <table>
            <thead>
              <tr>
                <th scope="col">Client ID</th>
                <th scope="col">Address</th>
                <th scope="col">Hostname</th>
                <th scope="col">Expires</th>
                <th scope="col">Active</th>
              </tr>
            </thead>
            <tbody>
              {leases.map((lease) => (
                <tr key={`${lease.client_id}-${lease.address}`}>
                  <td class="mono">{lease.client_id || "—"}</td>
                  <td class="mono">{lease.address || "—"}</td>
                  <td>{lease.hostname || "—"}</td>
                  <td>{formatDate(lease.expires_at)}</td>
                  <td>
                    <span class="status-badge" data-status={lease.active ? "running" : "disabled"}>
                      {lease.active ? "active" : "expired"}
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : (
          <div class="empty">
            <div class="empty-mark" aria-hidden="true">
              ◌
            </div>
            <h3>No leases yet</h3>
            <p>Clients will appear here after they receive an address from this VNF.</p>
          </div>
        )}
      </div>

      <footer class="dialog-actions">
        <button class="button ghost" type="button" disabled={loading} onClick={() => void load()}>
          {loading ? "Refreshing…" : "Refresh"}
        </button>
        <button class="button primary" type="button" onClick={() => dialogRef.current?.close()}>
          Close
        </button>
      </footer>
    </dialog>
  );
}
