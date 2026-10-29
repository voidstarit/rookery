import { useEffect, useRef, useState } from "preact/hooks";
import { api } from "../../shared/api";
import type { Network } from "../types";

type NetworkDialogProps = {
  network?: Network | null;
  onClose: () => void;
  onSaved: (network: Network) => void;
};

const NAME_PATTERN = "[A-Za-z0-9][A-Za-z0-9._\\-]*";

export function NetworkDialog({ network, onClose, onSaved }: NetworkDialogProps) {
  const editing = Boolean(network);
  const [name, setName] = useState(network?.name || "");
  const [ipv4CIDR, setIpv4CIDR] = useState(network?.ipv4_cidr || "10.0.0.0/16");
  const [ipv6CIDR, setIpv6CIDR] = useState(network?.ipv6_cidr || "fd00:20::/64");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const dialogRef = useRef<HTMLDialogElement | null>(null);
  const nameRef = useRef<HTMLInputElement | null>(null);
  const formRef = useRef<HTMLFormElement | null>(null);

  useEffect(() => {
    dialogRef.current?.showModal();
    nameRef.current?.focus();
  }, []);

  const close = () => {
    if (!saving) {
      dialogRef.current?.close();
    }
  };

  const submit = async (event: Event) => {
    event.preventDefault();
    if (saving || !formRef.current?.reportValidity()) {
      return;
    }
    setError("");
    setSaving(true);
    try {
      const saved = await api<Network>(
        editing && network ? `/api/networks/${network.id}` : "/api/networks",
        {
          method: editing ? "PUT" : "POST",
          body: JSON.stringify(
            editing
              ? { ipv4_cidr: ipv4CIDR.trim(), ipv6_cidr: ipv6CIDR.trim() }
              : { name: name.trim(), ipv4_cidr: ipv4CIDR.trim(), ipv6_cidr: ipv6CIDR.trim() }
          ),
        }
      );
      onSaved(saved);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  };

  return (
    <dialog
      ref={dialogRef}
      id="network-dialog"
      aria-labelledby="network-dialog-title"
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
            <p class="eyebrow">VPP bridge domain</p>
            <h2 id="network-dialog-title">{editing ? "Edit Rookery Network" : "Create Rookery Network"}</h2>
          </div>
          <button class="icon-button" type="button" aria-label="Close dialog" onClick={close}>
            ×
          </button>
        </header>

        <div class="form-grid">
          <label class="field field-wide">
            <span class="field-label">Name</span>
            <input
              ref={nameRef}
              type="text"
              required
              maxlength={64}
              pattern={NAME_PATTERN}
              autocomplete="off"
              spellcheck={false}
              placeholder="lab-net"
              value={name}
              disabled={editing}
              onInput={(event) => setName(event.currentTarget.value)}
            />
            <span class="field-hint">
              {editing ? "The network name is immutable." : "Letters, digits, dots, dashes and underscores."}
            </span>
          </label>

          <label class="field">
            <span class="field-label">IPv4 network CIDR</span>
            <input
              type="text"
              required
              autocomplete="off"
              spellcheck={false}
              placeholder="10.0.0.0/16"
              value={ipv4CIDR}
              onInput={(event) => setIpv4CIDR(event.currentTarget.value)}
            />
            <span class="field-hint">
              Address range for this network; .1 is the BVI and .2 the VNF stack.
              {editing ? " CIDRs can only be changed while the network has no VNFs, host taps, or BVI." : ""}
            </span>
          </label>

          <label class="field">
            <span class="field-label">IPv6 network CIDR</span>
            <input
              type="text"
              required
              autocomplete="off"
              spellcheck={false}
              placeholder="fd00:20::/64"
              value={ipv6CIDR}
              onInput={(event) => setIpv6CIDR(event.currentTarget.value)}
            />
            <span class="field-hint">IPv6 range reserved for this network.</span>
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
          <button class="button primary" type="submit" disabled={saving}>
            {saving ? "Creating…" : "Create network"}
          </button>
        </footer>
      </form>
    </dialog>
  );
}
