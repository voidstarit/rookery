import { useEffect, useRef, useState } from "preact/hooks";
import { api } from "../../shared/api";
import type { VNF, VNFBGPStatus } from "../types";

type VNFBGPStatusDialogProps = {
  vnf: VNF;
  onClose: () => void;
};

function sessionLabel(state: string): string {
  return state.replace(/^SESSION_STATE_/, "").toLowerCase();
}

export function VNFBGPStatusDialog({ vnf, onClose }: VNFBGPStatusDialogProps) {
  const [status, setStatus] = useState<VNFBGPStatus | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const dialogRef = useRef<HTMLDialogElement | null>(null);

  const load = async () => {
    setLoading(true);
    try {
      const data = await api<VNFBGPStatus>(`/api/vnfs/${vnf.id}/bgp`);
      setStatus(data);
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

  const session = status?.session_state || "";
  const stateLabel = status
    ? status.established
      ? "established"
      : status.running
        ? sessionLabel(session) || "starting"
        : "not running"
    : "loading";

  return (
    <dialog
      ref={dialogRef}
      class="vnf-bgp-dialog"
      aria-labelledby="vnf-bgp-title"
      onClose={onClose}
      onClick={(event) => {
        if (event.target === event.currentTarget) {
          dialogRef.current?.close();
        }
      }}
    >
      <header class="dialog-head">
        <div>
          <p class="eyebrow">BGP monitor</p>
          <h2 id="vnf-bgp-title">BGP status · {vnf.name}</h2>
        </div>
        <button class="icon-button" type="button" aria-label="Close dialog" onClick={() => dialogRef.current?.close()}>
          ×
        </button>
      </header>

      <p class="dialog-lede">
        {vnf.network_name || "network"} · local AS {vnf.config.local_as || "—"} · peer AS{" "}
        {vnf.config.peer_as || "—"}
      </p>

      {error ? (
        <div class="banner" role="alert">
          {error}
        </div>
      ) : null}

      {status ? (
        <>
          <div class="bgp-summary">
            <span class="status-badge" data-status={status.established ? "running" : status.running ? "degraded" : "disabled"}>
              {stateLabel}
            </span>
            <span class="bgp-peer">
              peer {status.peer_address || vnf.config.peer_ip || "—"}
              {status.peer_as ? ` · AS${status.peer_as}` : ""}
            </span>
            <span class="bgp-local">local AS{status.local_as || "—"}</span>
          </div>

          <h3 class="bgp-section-title">Advertised prefixes</h3>
          <div class="table-wrap">
            {status.advertised && status.advertised.length > 0 ? (
              <table>
                <thead>
                  <tr>
                    <th scope="col">Prefix</th>
                  </tr>
                </thead>
                <tbody>
                  {status.advertised.map((prefix) => (
                    <tr key={prefix}>
                      <td class="mono">{prefix}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            ) : (
              <div class="empty">
                <p>No advertised prefixes.</p>
              </div>
            )}
          </div>

          <h3 class="bgp-section-title">Received prefixes</h3>
          <div class="table-wrap">
            {status.received && status.received.length > 0 ? (
              <table>
                <thead>
                  <tr>
                    <th scope="col">Prefix</th>
                    <th scope="col">Next hop</th>
                    <th scope="col">AS path</th>
                  </tr>
                </thead>
                <tbody>
                  {status.received.map((prefix) => (
                    <tr key={prefix.prefix}>
                      <td class="mono">{prefix.prefix}</td>
                      <td class="mono">{prefix.next_hop || "—"}</td>
                      <td class="mono">
                        {prefix.as_path && prefix.as_path.length > 0
                          ? prefix.as_path.join(" ")
                          : "—"}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            ) : (
              <div class="empty">
                <p>No received prefixes.</p>
              </div>
            )}
          </div>
        </>
      ) : (
        <p class="dialog-lede">{loading ? "Loading…" : "BGP runtime not running."}</p>
      )}

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
