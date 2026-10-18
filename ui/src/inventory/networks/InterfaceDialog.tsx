import { useEffect, useRef, useState } from "preact/hooks";
import { api } from "../../shared/api";
import type { Network, VirtualInterface, VM } from "../types";

type InterfaceDialogProps = {
  networks: Network[];
  vms: VM[];
  onClose: () => void;
  onSaved: (iface: VirtualInterface) => void;
};

const NAME_PATTERN = "[A-Za-z0-9][A-Za-z0-9._-]*";

export function InterfaceDialog({ networks, vms, onClose, onSaved }: InterfaceDialogProps) {
  const [name, setName] = useState("");
  const [networkID, setNetworkID] = useState(networks[0] ? String(networks[0].id) : "");
  const [vmID, setVMID] = useState("");
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
      const iface = await api<VirtualInterface>("/api/interfaces", {
        method: "POST",
        body: JSON.stringify({
          name: name.trim(),
          network_id: Number(networkID),
          attached_vm_id: vmID || null,
        }),
      });
      onSaved(iface);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  };

  return (
    <dialog
      ref={dialogRef}
      id="interface-dialog"
      aria-labelledby="interface-dialog-title"
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
            <p class="eyebrow">VPP vhost-user</p>
            <h2 id="interface-dialog-title">Add virtual interface</h2>
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
              placeholder="vm01-eth0"
              value={name}
              onInput={(event) => setName(event.currentTarget.value)}
            />
            <span class="field-hint">Creates a vhost-user socket under the configured VPP directory.</span>
          </label>

          <label class="field field-wide">
            <span class="field-label">Rookery Network</span>
            <select
              required
              value={networkID}
              onChange={(event) => setNetworkID(event.currentTarget.value)}
            >
              {networks.map((network) => (
                <option value={String(network.id)} key={network.id}>
                  {network.name} · bd {network.bridge_domain_id}
                </option>
              ))}
            </select>
          </label>

          <label class="field field-wide">
            <span class="field-label">Attach to VM <em>optional</em></span>
            <select value={vmID} onChange={(event) => setVMID(event.currentTarget.value)}>
              <option value="">Leave unattached</option>
              {vms.map((vm) => {
                const running = vm.status === "running" || vm.status === "starting";
                return (
                  <option value={vm.id} key={vm.id}>
                    {running ? `${vm.name} (running — stop first)` : vm.name}
                  </option>
                );
              })}
            </select>
            <span class="field-hint">Stop the VM before attaching or detaching interfaces.</span>
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
            {saving ? "Creating…" : "Add interface"}
          </button>
        </footer>
      </form>
    </dialog>
  );
}
