import { useEffect, useRef, useState } from "preact/hooks";
import { api } from "../../shared/api";
import { LabelsInput, saveEntityLabelsWithToast, useEntityLabelDraft } from "../../shared/labels";
import type { Network, NetworkPipe } from "../types";

type NetworkPipeDialogProps = {
  networks: Network[];
  onClose: () => void;
  onSaved: (pipe: NetworkPipe) => void;
};

export function NetworkPipeDialog({ networks, onClose, onSaved }: NetworkPipeDialogProps) {
  const [networkA, setNetworkA] = useState(networks[0] ? String(networks[0].id) : "");
  const [networkB, setNetworkB] = useState(networks[1] ? String(networks[1].id) : "");
  const [labels, setLabels] = useEntityLabelDraft("network_pipe");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const dialogRef = useRef<HTMLDialogElement | null>(null);
  const formRef = useRef<HTMLFormElement | null>(null);

  useEffect(() => {
    dialogRef.current?.showModal();
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
    if (!networkA || !networkB || networkA === networkB) {
      setError("Choose two different networks.");
      return;
    }
    setSaving(true);
    setError("");
    try {
      const pipe = await api<NetworkPipe>("/api/network-pipes", {
        method: "POST",
        body: JSON.stringify({ network_a_id: Number(networkA), network_b_id: Number(networkB) }),
      });
      await saveEntityLabelsWithToast("network_pipe", pipe.id, `pipes for ${pipe.network_a_name} and ${pipe.network_b_name}`, labels);
      onSaved(pipe);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  };

  return (
    <dialog
      ref={dialogRef}
      id="network-pipe-dialog"
      aria-labelledby="network-pipe-title"
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
            <p class="eyebrow">Bridge-domain pipe</p>
            <h2 id="network-pipe-title">Connect two networks</h2>
          </div>
          <button class="icon-button" type="button" aria-label="Close dialog" onClick={close}>
            ×
          </button>
        </header>

        <p class="field-hint">
          Creates a VPP pipe pair and places one end in each bridge domain. This bridges L2
          traffic between the two Rookery Networks.
        </p>

        <div class="form-grid">
          <label class="field">
            <span class="field-label">Network A</span>
            <select required value={networkA} onChange={(event) => setNetworkA(event.currentTarget.value)}>
              {networks.map((network) => (
                <option value={String(network.id)} key={network.id}>{network.name}</option>
              ))}
            </select>
          </label>
          <label class="field">
            <span class="field-label">Network B</span>
            <select required value={networkB} onChange={(event) => setNetworkB(event.currentTarget.value)}>
              {networks.map((network) => (
                <option value={String(network.id)} key={network.id}>{network.name}</option>
              ))}
            </select>
          </label>
          <div class="field field-wide">
            <LabelsInput labels={labels} onChange={setLabels} disabled={saving} />
          </div>
        </div>

        {networks.length < 2 ? (
          <p class="field-hint">Create at least two networks before adding a pipe.</p>
        ) : null}
        {error ? <p class="form-error" role="alert">{error}</p> : null}

        <footer class="dialog-actions">
          <button class="button ghost" type="button" disabled={saving} onClick={close}>
            Cancel
          </button>
          <button class="button primary" type="submit" disabled={saving || networks.length < 2}>
            {saving ? "Creating…" : "Create pipe"}
          </button>
        </footer>
      </form>
    </dialog>
  );
}
