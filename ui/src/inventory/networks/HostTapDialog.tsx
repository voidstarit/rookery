import { useEffect, useRef, useState } from "preact/hooks";
import { api } from "../../shared/api";
import type { HostTap, Network } from "../types";

type HostTapDialogProps = {
  networks: Network[];
  onClose: () => void;
  onSaved: (tap: HostTap) => void;
};

const NAME_PATTERN = "[A-Za-z0-9][A-Za-z0-9._\\-]*";

export function HostTapDialog({ networks, onClose, onSaved }: HostTapDialogProps) {
  const [networkID, setNetworkID] = useState(networks[0] ? String(networks[0].id) : "");
  const [name, setName] = useState("");
  const [ipv4Address, setIpv4Address] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const dialogRef = useRef<HTMLDialogElement | null>(null);
  const networkRef = useRef<HTMLSelectElement | null>(null);
  const formRef = useRef<HTMLFormElement | null>(null);

  useEffect(() => {
    dialogRef.current?.showModal();
    networkRef.current?.focus();
  }, []);

  const close = () => {
    if (!saving) {
      dialogRef.current?.close();
    }
  };

  const selectedNetwork = networks.find((network) => String(network.id) === networkID);

  const submit = async (event: Event) => {
    event.preventDefault();
    if (saving || !formRef.current?.reportValidity()) {
      return;
    }
    setError("");
    setSaving(true);
    try {
      const tap = await api<HostTap>("/api/host/taps", {
        method: "POST",
        body: JSON.stringify({
          network_id: Number(networkID),
          name: name.trim(),
          ipv4_address: ipv4Address.trim(),
        }),
      });
      onSaved(tap);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  };

  return (
    <dialog
      ref={dialogRef}
      id="host-tap-dialog"
      aria-labelledby="host-tap-dialog-title"
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
      <form ref={formRef} novalidate onSubmit={(event) => void submit(event)}>
        <header class="dialog-head">
          <div>
            <p class="eyebrow">Network to host bridge</p>
            <h2 id="host-tap-dialog-title">Add host tap</h2>
          </div>
          <button class="icon-button" type="button" aria-label="Close dialog" onClick={close}>
            ×
          </button>
        </header>

        <div class="form-grid">
          <label class="field field-wide">
            <span class="field-label">Rookery Network</span>
            <select
              ref={networkRef}
              required
              value={networkID}
              onChange={(event) => setNetworkID(event.currentTarget.value)}
            >
              {networks.map((network) => (
                <option key={network.id} value={network.id}>
                  {network.name}
                  {network.ipv4_cidr ? ` · ${network.ipv4_cidr}` : ""}
                </option>
              ))}
            </select>
            <span class="field-hint">
              The tap is bridged into this network, so the host sees its machines at layer 2.
            </span>
          </label>

          <label class="field">
            <span class="field-label">Linux interface name</span>
            <input
              type="text"
              maxlength={15}
              pattern={NAME_PATTERN}
              autocomplete="off"
              spellcheck={false}
              placeholder={selectedNetwork ? `hostnet${selectedNetwork.id}` : "hostnet1"}
              value={name}
              onInput={(event) => setName(event.currentTarget.value)}
            />
            <span class="field-hint">Optional; defaults to hostnet&lt;network id&gt;. Maximum 15 characters.</span>
          </label>

          <label class="field">
            <span class="field-label">Host IPv4 address</span>
            <input
              type="text"
              autocomplete="off"
              spellcheck={false}
              placeholder="10.0.0.50/16"
              value={ipv4Address}
              onInput={(event) => setIpv4Address(event.currentTarget.value)}
            />
            <span class="field-hint">
              Optional CIDR inside the network, outside the first five reserved addresses.
              Leave empty to use DHCP or configure the interface manually.
            </span>
          </label>
        </div>

        {error ? (
          <p class="form-error" role="alert">
            {error}
          </p>
        ) : null}

        <footer class="dialog-actions">
          <button class="button ghost" type="button" disabled={saving} onClick={close}>
            Cancel
          </button>
          <button class="button primary" type="submit" disabled={saving || !networkID}>
            {saving ? "Creating…" : "Create host tap"}
          </button>
        </footer>
      </form>
    </dialog>
  );
}
