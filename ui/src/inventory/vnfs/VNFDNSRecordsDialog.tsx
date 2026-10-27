import { useEffect, useRef, useState } from "preact/hooks";
import { api } from "../../shared/api";
import type { VNF, VNFDNSRecordsResponse } from "../types";

type VNFDNSRecordsDialogProps = {
  vnf: VNF;
  onClose: () => void;
};

function sourceLabel(source: string, sourceVNFID?: number): string {
  if (source === "static") {
    return "static";
  }
  if (source === "dhcp") {
    return sourceVNFID ? `DHCP #${sourceVNFID}` : "DHCP";
  }
  return source || "—";
}

export function VNFDNSRecordsDialog({ vnf, onClose }: VNFDNSRecordsDialogProps) {
  const [data, setData] = useState<VNFDNSRecordsResponse | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const dialogRef = useRef<HTMLDialogElement | null>(null);

  const load = async () => {
    setLoading(true);
    try {
      const response = await api<VNFDNSRecordsResponse>(`/api/vnfs/${vnf.id}/dns-records`);
      setData(response);
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

  const records = data?.records || [];

  return (
    <dialog
      ref={dialogRef}
      class="vnf-bgp-dialog"
      aria-labelledby="vnf-dns-records-title"
      onClose={onClose}
      onClick={(event) => {
        if (event.target === event.currentTarget) {
          dialogRef.current?.close();
        }
      }}
    >
      <header class="dialog-head">
        <div>
          <p class="eyebrow">DNS records</p>
          <h2 id="vnf-dns-records-title">DNS records · {vnf.name}</h2>
        </div>
        <button class="icon-button" type="button" aria-label="Close dialog" onClick={() => dialogRef.current?.close()}>
          ×
        </button>
      </header>

      <p class="dialog-lede">
        {vnf.network_name || "network"} · {records.length} record{records.length === 1 ? "" : "s"} · static and
        DHCP-derived
      </p>

      {error ? (
        <div class="banner" role="alert">
          {error}
        </div>
      ) : null}
      {data?.dynamic_error ? (
        <div class="banner" role="alert">
          Dynamic records unavailable: {data.dynamic_error}
        </div>
      ) : null}

      <div class="table-wrap">
        {records.length > 0 ? (
          <table>
            <thead>
              <tr>
                <th scope="col">Name</th>
                <th scope="col">Type</th>
                <th scope="col">Value</th>
                <th scope="col">TTL</th>
                <th scope="col">Source</th>
              </tr>
            </thead>
            <tbody>
              {records.map((record, index) => (
                <tr key={`${record.name}-${record.type}-${record.value}-${index}`}>
                  <td class="mono">{record.name}</td>
                  <td class="mono">{record.type}</td>
                  <td class="mono">{record.value}</td>
                  <td class="mono">{record.ttl || "—"}</td>
                  <td>{sourceLabel(record.source, record.source_vnf_id)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : (
          <div class="empty">
            <p>{loading ? "Loading records…" : "No DNS records."}</p>
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
