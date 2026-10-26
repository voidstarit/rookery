import { useEffect, useRef, useState } from "preact/hooks";
import { api } from "../../shared/api";
import { showToast } from "../../shared/toast";

type VPPAddresses = {
  name: string;
  addresses?: string[];
  dhcp_client_enabled?: boolean;
  live_addresses?: string[];
  dhcp_client_configured?: boolean;
  dhcp_client_status?: string;
  status_detail?: string;
};

type VPPAddressesDialogProps = {
  name: string;
  label?: string;
  hostAddresses?: string[];
  onClose: () => void;
};

export function VPPAddressesDialog({ name, label, hostAddresses, onClose }: VPPAddressesDialogProps) {
  const [text, setText] = useState("");
  const [dhcpEnabled, setDhcpEnabled] = useState(false);
  const [liveAddresses, setLiveAddresses] = useState<string[]>([]);
  const [dhcpConfigured, setDhcpConfigured] = useState(false);
  const [dhcpStatus, setDhcpStatus] = useState("");
  const [statusDetail, setStatusDetail] = useState("");
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const dialogRef = useRef<HTMLDialogElement | null>(null);
  const textRef = useRef<HTMLTextAreaElement | null>(null);
  const endpoint = `/api/vpp/interfaces/${encodeURIComponent(name)}/addresses`;

  const applyLive = (data: VPPAddresses) => {
    setLiveAddresses(data.live_addresses || []);
    setDhcpConfigured(Boolean(data.dhcp_client_configured));
    setDhcpStatus(data.dhcp_client_status || "");
    setStatusDetail(data.status_detail || "");
  };

  useEffect(() => {
    dialogRef.current?.showModal();
    void load();
  }, []);

  const load = async () => {
    setLoading(true);
    try {
      const data = await api<VPPAddresses>(endpoint);
      setText((data.addresses || []).join("\n"));
      setDhcpEnabled(Boolean(data.dhcp_client_enabled));
      applyLive(data);
      setError("");
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
      textRef.current?.focus();
    }
  };

  const refreshStatus = async () => {
    if (refreshing) {
      return;
    }
    setRefreshing(true);
    try {
      const data = await api<VPPAddresses>(endpoint);
      applyLive(data);
      setError("");
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setRefreshing(false);
    }
  };

  const close = () => {
    if (!saving) {
      dialogRef.current?.close();
    }
  };

  const submit = async (event: Event) => {
    event.preventDefault();
    if (saving || loading) {
      return;
    }
    setSaving(true);
    setError("");
    const addresses = text
      .split(/\r?\n/)
      .map((value) => value.trim())
      .filter(Boolean);
    try {
      const updated = await api<VPPAddresses>(endpoint, {
        method: "PUT",
        body: JSON.stringify({ addresses, dhcp_client_enabled: dhcpEnabled }),
      });
      setText((updated.addresses || []).join("\n"));
      setDhcpEnabled(Boolean(updated.dhcp_client_enabled));
      applyLive(updated);
      showToast(`Saved configuration for ${name}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  };

  return (
    <dialog
      ref={dialogRef}
      id="vpp-addresses-dialog"
      aria-labelledby="vpp-addresses-title"
      onClose={onClose}
      onClick={(event) => {
        if (event.target === event.currentTarget) {
          close();
        }
      }}
      onCancel={(event) => {
        if (saving) {
          event.preventDefault();
        }
      }}
    >
      <form onSubmit={(event) => void submit(event)}>
        <header class="dialog-head">
          <div>
            <p class="eyebrow">VPP interface addresses</p>
            <h2 id="vpp-addresses-title">Addresses · {name}</h2>
          </div>
          <button class="icon-button" type="button" aria-label="Close dialog" onClick={close}>
            ×
          </button>
        </header>

        <div class="startup-script-status">
          <span class="status-badge" data-status="running">{label || "VPP interface"}</span>
          <span class="field-hint">IPv4 and IPv6 CIDRs are applied directly to this VPP interface.</span>
        </div>

        <label class="field field-wide checkbox-field">
          <span class="checkbox-row">
            <input
              type="checkbox"
              checked={dhcpEnabled}
              onChange={(event) => setDhcpEnabled(event.currentTarget.checked)}
            />
            <span>Enable DHCP client (IPv4)</span>
          </span>
          <span class="field-hint">
            VPP requests an IPv4 lease from the network. This can be combined with static
            addresses.
          </span>
        </label>

        {hostAddresses ? (
          <div class="vpp-address-live field-wide">
            <div class="vpp-address-live-head">
              <span class="field-label">Host side addresses</span>
            </div>
            {hostAddresses.length > 0 ? (
              <div class="vpp-live-addresses mono">
                {hostAddresses.map((address) => (
                  <span key={address}>{address}</span>
                ))}
              </div>
            ) : (
              <p class="field-hint">No host-side address assigned.</p>
            )}
          </div>
        ) : null}

        <div class="vpp-address-live field-wide">
          <div class="vpp-address-live-head">
            <span class="field-label">VPP side addresses</span>
            <div class="actions">
              <span
                class="status-badge"
                data-status={dhcpConfigured ? "running" : "disabled"}
              >
                DHCP client {dhcpConfigured ? "configured" : "not configured"}
              </span>
              <button
                class="row-action"
                type="button"
                disabled={refreshing || saving}
                onClick={() => void refreshStatus()}
              >
                {refreshing ? "Refreshing…" : "Refresh status"}
              </button>
            </div>
          </div>
          {statusDetail ? (
            <p class="field-hint">{statusDetail}</p>
          ) : liveAddresses.length > 0 ? (
            <div class="vpp-live-addresses mono">
              {liveAddresses.map((address) => (
                <span key={address}>{address}</span>
              ))}
            </div>
          ) : (
            <p class="field-hint">No live address assigned yet{ dhcpEnabled ? "; waiting for a DHCP lease." : "." }</p>
          )}
          {dhcpStatus ? (
            <details class="startup-script-output">
              <summary>DHCP client status</summary>
              <pre>{dhcpStatus}</pre>
            </details>
          ) : null}
        </div>

        <label class="field field-wide">
          <span class="field-label">Addresses</span>
          <textarea
            ref={textRef}
            class="startup-script-editor"
            rows={8}
            spellcheck={false}
            placeholder={"10.20.0.10/24\nfd00:20::10/64"}
            value={text}
            onInput={(event) => setText(event.currentTarget.value)}
          />
          <span class="field-hint">
            One address per line, using CIDR notation. Leave empty to remove all Rookery-managed
            addresses. Existing addresses managed by other Rookery features are not shown here.
          </span>
        </label>

        {error ? (
          <p class="form-error" role="alert">
            {error}
          </p>
        ) : null}

        <footer class="dialog-actions">
          <button class="button ghost" type="button" disabled={saving} onClick={close}>
            Close
          </button>
          <button class="button primary" type="submit" disabled={saving || loading}>
            {saving ? "Saving…" : "Save addresses"}
          </button>
        </footer>
      </form>
    </dialog>
  );
}
