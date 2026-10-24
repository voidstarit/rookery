import { useEffect, useRef, useState } from "preact/hooks";
import { APIError, api } from "../shared/api";
import { showToast } from "../shared/toast";

type ClientCertificate = {
  id: number;
  name: string;
  serial: string;
  not_before: string;
  not_after: string;
  revoked: boolean;
  created_at: string;
};

type IssuedCertificate = {
  id: number;
  name: string;
  serial: string;
  not_before: string;
  not_after: string;
  certificate_pem: string;
  private_key_pem: string;
  ca_pem: string;
};

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

function downloadPEM(name: string, content: string): void {
  const blob = new Blob([content], { type: "application/x-pem-file" });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = name;
  anchor.click();
  URL.revokeObjectURL(url);
}

export function ClientCertificates() {
  const dialogRef = useRef<HTMLDialogElement | null>(null);
  const [open, setOpen] = useState(false);
  const [available, setAvailable] = useState(true);
  const [clients, setClients] = useState<ClientCertificate[]>([]);
  const [name, setName] = useState("");
  const [days, setDays] = useState("365");
  const [issued, setIssued] = useState<IssuedCertificate | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!open) {
      dialogRef.current?.close();
      return;
    }
    dialogRef.current?.showModal();
    setIssued(null);
    setError("");
    void loadClients();
  }, [open]);

  const loadClients = async () => {
    setLoading(true);
    try {
      // Use a direct fetch here so a disabled mTLS listener fails immediately;
      // the shared api() helper would retry a 503 for several seconds.
      const response = await fetch("/api/pki/clients", { headers: { Accept: "application/json" } });
      const text = await response.text();
      let payload: unknown = null;
      if (text) {
        try {
          payload = JSON.parse(text);
        } catch {
          payload = null;
        }
      }
      if (!response.ok) {
        const message =
          payload && typeof payload === "object" && "error" in payload && typeof payload.error === "string"
            ? payload.error
            : `Request failed (${response.status})`;
        throw new APIError(message, response.status);
      }
      setClients(Array.isArray(payload) ? (payload as ClientCertificate[]) : []);
      setAvailable(true);
      setError("");
    } catch (err) {
      if (err instanceof APIError && err.status === 503) {
        setAvailable(false);
        setError("mTLS is not enabled on this server. Start rookery with -mtls-addr and -pki-dir.");
      } else {
        setError(err instanceof Error ? err.message : String(err));
      }
    } finally {
      setLoading(false);
    }
  };

  const issue = async (event: Event) => {
    event.preventDefault();
    if (busy || !name.trim()) {
      return;
    }
    setBusy(true);
    setError("");
    try {
      const created = await api<IssuedCertificate>("/api/pki/clients", {
        method: "POST",
        body: JSON.stringify({ name: name.trim(), days: Number(days) || 365 }),
      });
      setIssued(created);
      setName("");
      await loadClients();
      showToast(`Issued client certificate for ${created.name}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  const revoke = async (certificate: ClientCertificate) => {
    if (!window.confirm(`Revoke the certificate for ${certificate.name}? Existing connections will fail on the next handshake.`)) {
      return;
    }
    setBusy(true);
    setError("");
    try {
      await api<void>(`/api/pki/clients/${certificate.id}`, { method: "DELETE" });
      await loadClients();
      showToast(`Revoked client certificate for ${certificate.name}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div class="sidebar-settings">
      <p class="eyebrow">Access</p>
      <button class="tab" type="button" onClick={() => setOpen(true)}>
        Client certificates
      </button>
      <span class="field-hint">Public mTLS API access</span>

      <dialog
        ref={dialogRef}
        class="dialog dialog-wide"
        aria-labelledby="client-certs-title"
        onClose={() => setOpen(false)}
        onClick={(event) => {
          if (event.target === event.currentTarget) {
            setOpen(false);
          }
        }}
        onCancel={(event) => {
          if (busy) {
            event.preventDefault();
          }
        }}
      >
        <div class="dialog-body">
        <header class="dialog-head">
          <div>
            <p class="eyebrow">mTLS access</p>
            <h2 id="client-certs-title">Client certificates</h2>
          </div>
          <button class="icon-button" type="button" aria-label="Close dialog" onClick={() => setOpen(false)}>
            ×
          </button>
        </header>

        {!available ? (
          <div class="banner" role="alert">
            {error || "mTLS is not enabled on this server."}
          </div>
        ) : (
          <>
            <p class="field-hint">
              Clients connect to the public mTLS listener and must present a certificate signed by this
              server's CA. Private keys are shown once and never stored by Rookery.
            </p>

            <div class="actions">
              <a class="button ghost" href="/api/pki/ca" download="rookery-ca.pem">
                Download CA certificate
              </a>
            </div>

            <form class="form-grid" onSubmit={(event) => void issue(event)}>
              <label class="field">
                <span class="field-label">Client name</span>
                <input
                  type="text"
                  required
                  maxlength={64}
                  autocomplete="off"
                  spellcheck={false}
                  placeholder="ops-laptop"
                  value={name}
                  onInput={(event) => setName(event.currentTarget.value)}
                />
                <span class="field-hint">Used as the certificate common name.</span>
              </label>
              <label class="field">
                <span class="field-label">Validity (days)</span>
                <input
                  type="number"
                  required
                  min={1}
                  max={3650}
                  value={days}
                  onInput={(event) => setDays(event.currentTarget.value)}
                />
              </label>
              <div class="field field-wide actions">
                <button class="button primary" type="submit" disabled={busy || !name.trim()}>
                  {busy ? "Issuing…" : "Issue certificate"}
                </button>
              </div>
            </form>
          </>
        )}

        {issued ? (
          <section class="panel panel-spaced" aria-labelledby="issued-cert-title">
            <div class="panel-head">
              <div>
                <h3 id="issued-cert-title">Certificate for {issued.name}</h3>
                <p class="panel-subtitle">Download or copy these files now; the private key will not be shown again.</p>
              </div>
            </div>
            <div class="actions">
              <button class="button primary" type="button" onClick={() => downloadPEM("client.crt", issued.certificate_pem)}>
                Download client.crt
              </button>
              <button class="button primary" type="button" onClick={() => downloadPEM("client.key", issued.private_key_pem)}>
                Download client.key
              </button>
              <button class="button ghost" type="button" onClick={() => downloadPEM("ca.crt", issued.ca_pem)}>
                Download ca.crt
              </button>
            </div>
            <label class="field field-wide">
              <span class="field-label">Client certificate (PEM)</span>
              <textarea class="cert-pem" readOnly rows={5} value={issued.certificate_pem} />
            </label>
            <label class="field field-wide">
              <span class="field-label">Client private key (PEM)</span>
              <textarea class="cert-pem" readOnly rows={5} value={issued.private_key_pem} />
            </label>
            <label class="field field-wide">
              <span class="field-label">CA certificate (PEM)</span>
              <textarea class="cert-pem" readOnly rows={4} value={issued.ca_pem} />
            </label>
          </section>
        ) : null}

        {error && available ? (
          <p class="form-error" role="alert">
            {error}
          </p>
        ) : null}

        {available ? (
          <div class="table-wrap">
            {loading ? (
              <p class="field-hint">Loading issued certificates…</p>
            ) : clients.length > 0 ? (
              <table>
                <thead>
                  <tr>
                    <th scope="col">Name</th>
                    <th scope="col">Serial</th>
                    <th scope="col">Expires</th>
                    <th scope="col">Status</th>
                    <th scope="col" />
                  </tr>
                </thead>
                <tbody>
                  {clients.map((certificate) => (
                    <tr key={certificate.id}>
                      <td>{certificate.name}</td>
                      <td class="mono">{certificate.serial.slice(0, 16)}…</td>
                      <td>{formatDate(certificate.not_after)}</td>
                      <td>
                        <span
                          class="status-badge"
                          data-status={certificate.revoked ? "disabled" : "running"}
                        >
                          {certificate.revoked ? "revoked" : "valid"}
                        </span>
                      </td>
                      <td>
                        {certificate.revoked ? null : (
                          <button
                            class="row-action danger"
                            type="button"
                            disabled={busy}
                            onClick={() => void revoke(certificate)}
                          >
                            Revoke
                          </button>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            ) : (
              <p class="field-hint">No client certificates have been issued yet.</p>
            )}
          </div>
        ) : null}

        <footer class="dialog-actions">
          <button class="button ghost" type="button" onClick={() => setOpen(false)}>
            Close
          </button>
        </footer>
        </div>
      </dialog>
    </div>
  );
}
