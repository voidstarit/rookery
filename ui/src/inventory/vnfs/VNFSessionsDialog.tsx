import { useEffect, useRef, useState } from "preact/hooks";
import { api } from "../../shared/api";
import type { NAT44SessionsResponse, NAT44VNF } from "../types";

type VNFSessionsDialogProps = {
  vnf: NAT44VNF;
  onClose: () => void;
};

/**
 * VNFSessionsDialog renders a modal preformatted list of NAT44 sessions for one VNF. It fetches
 * /api/nat44-vnfs/{id}/sessions on mount and Refresh, reports load errors in a banner, and falls
 * back to loading or empty text while there are no sessions.
 */
export function VNFSessionsDialog({ vnf, onClose }: VNFSessionsDialogProps) {
  const [sessions, setSessions] = useState<string[]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const dialogRef = useRef<HTMLDialogElement | null>(null);

  const load = async () => {
    setLoading(true);
    try {
      const data = await api<NAT44SessionsResponse>(`/api/nat44-vnfs/${vnf.id}/sessions`);
      setSessions(Array.isArray(data.sessions) ? data.sessions : []);
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
      class="vnf-sessions-dialog"
      aria-labelledby="vnf-sessions-title"
      onClose={onClose}
      onClick={(event) => {
        if (event.target === event.currentTarget) {
          dialogRef.current?.close();
        }
      }}
    >
      <header class="dialog-head">
        <div>
          <p class="eyebrow">NAT monitor</p>
          <h2 id="vnf-sessions-title">NAT44 sessions · {vnf.name}</h2>
        </div>
        <button class="icon-button" type="button" aria-label="Close dialog" onClick={() => dialogRef.current?.close()}>
          ×
        </button>
      </header>

      <p class="dialog-lede">
        {vnf.network_name || "network"} · outside {vnf.outside_interface || "—"}
      </p>
      {error ? (
        <div class="banner" role="alert">
          {error}
        </div>
      ) : null}

      <pre class="nat-sessions">
        {sessions.length > 0 ? sessions.join("\n") : loading ? "Loading…" : "No active NAT44 sessions."}
      </pre>

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
