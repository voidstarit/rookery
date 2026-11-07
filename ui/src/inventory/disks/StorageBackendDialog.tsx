import { useEffect, useRef, useState } from "preact/hooks";
import { api } from "../../shared/api";
import { LabelsInput, saveEntityLabelsWithToast, useEntityLabelDraft } from "../../shared/labels";
import type { StorageBackend } from "../types";

type StorageBackendDialogProps = {
  backend: StorageBackend | null;
  onClose: () => void;
  onSaved: (backend: StorageBackend) => void;
};

const NAME_PATTERN = "[A-Za-z0-9][A-Za-z0-9._\\-]*";

export function StorageBackendDialog({ backend, onClose, onSaved }: StorageBackendDialogProps) {
  const editing = backend !== null;
  const [name, setName] = useState(backend?.name ?? "");
  const [title, setTitle] = useState(backend?.title ?? "");
  const [description, setDescription] = useState(backend?.description ?? "");
  const [driver, setDriver] = useState(backend?.driver ?? "lvm-thin");
  const [vg, setVG] = useState(backend?.config?.vg ?? "");
  const [thinPool, setThinPool] = useState(backend?.config?.thin_pool ?? "");
  const [supportsTemplates, setSupportsTemplates] = useState(Boolean(backend?.config?.supports_templates));
  const [preparationScript, setPreparationScript] = useState(backend?.config?.preparation_script ?? "");
  const [preparationTimeout, setPreparationTimeout] = useState(backend?.config?.preparation_timeout || "5m");
  const [allowTemplateDelete, setAllowTemplateDelete] = useState(Boolean(backend?.config?.allow_template_delete_with_clones));
  const [endpoint, setEndpoint] = useState(backend?.config?.kouja?.endpoint ?? "");
  const [protocol, setProtocol] = useState(backend?.config?.kouja?.protocol ?? "iscsi");
  const [storageClass, setStorageClass] = useState(backend?.config?.kouja?.storage_class ?? "");
  const [initiator, setInitiator] = useState(backend?.config?.kouja?.initiator ?? "");
  const [hostNQN, setHostNQN] = useState(backend?.config?.kouja?.host_nqn ?? "");
  const [caFile, setCAFile] = useState(backend?.config?.kouja?.ca_file ?? "");
  const [clientCertFile, setClientCertFile] = useState(backend?.config?.kouja?.client_cert_file ?? "");
  const [clientKeyFile, setClientKeyFile] = useState(backend?.config?.kouja?.client_key_file ?? "");
  const [serverName, setServerName] = useState(backend?.config?.kouja?.server_name ?? "");
  const [portal, setPortal] = useState(backend?.config?.kouja?.portal ?? "");
  const [labels, setLabels] = useEntityLabelDraft("storage_backend", backend?.id);
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
      const saved = await api<StorageBackend>(
        editing ? `/api/storage-backends/${backend?.id}` : "/api/storage-backends",
        {
          method: editing ? "PUT" : "POST",
          body: JSON.stringify({
            name: name.trim(),
            title: title.trim(),
            description: description.trim(),
            driver,
            config:
              driver === "kouja"
                ? {
                    kouja: {
                      endpoint: endpoint.trim(),
                      protocol,
                      storage_class: storageClass.trim(),
                      initiator: initiator.trim(),
                      host_nqn: hostNQN.trim(),
                      ca_file: caFile.trim(),
                      client_cert_file: clientCertFile.trim(),
                      client_key_file: clientKeyFile.trim(),
                      server_name: serverName.trim(),
                      portal: portal.trim(),
                    },
                  }
                : {
                    vg: vg.trim(),
                    thin_pool: thinPool.trim(),
                    supports_templates: supportsTemplates,
                    preparation_script: supportsTemplates ? preparationScript.trim() : "",
                    preparation_timeout: supportsTemplates ? preparationTimeout.trim() || "5m" : "",
                    allow_template_delete_with_clones: supportsTemplates && allowTemplateDelete,
                  },
          }),
        }
      );
      await saveEntityLabelsWithToast("storage_backend", saved.id, saved.name, labels);
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
      id="storage-backend-dialog"
      aria-labelledby="storage-backend-dialog-title"
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
            <p class="eyebrow">{editing ? "Edit backend" : "New backend"}</p>
            <h2 id="storage-backend-dialog-title">
              {editing ? `Storage backend · ${backend?.name}` : "Register storage backend"}
            </h2>
          </div>
          <button class="icon-button" type="button" aria-label="Close dialog" onClick={close}>
            ×
          </button>
        </header>

        <div class="form-grid">
          <label class="field">
            <span class="field-label">Name</span>
            <input
              ref={nameRef}
              type="text"
              required
              maxlength={64}
              pattern={NAME_PATTERN}
              autocomplete="off"
              spellcheck={false}
              placeholder="fast-pool"
              value={name}
              onInput={(event) => setName(event.currentTarget.value)}
            />
            <span class="field-hint">Used when creating disks.</span>
          </label>
          <label class="field">
            <span class="field-label">Title</span>
            <input
              type="text"
              maxlength={128}
              autocomplete="off"
              placeholder="Fast NVMe thin pool"
              value={title}
              onInput={(event) => setTitle(event.currentTarget.value)}
            />
          </label>
          <label class="field field-wide">
            <span class="field-label">Description <em>optional</em></span>
            <textarea
              rows={2}
              maxlength={2000}
              placeholder="What this backend is for"
              value={description}
              onInput={(event) => setDescription(event.currentTarget.value)}
            />
          </label>
          <label class="field">
            <span class="field-label">Driver</span>
            <select required value={driver} onChange={(event) => setDriver(event.currentTarget.value)}>
              <option value="lvm-thin">LVM thin pool</option>
              <option value="kouja">Kouja</option>
            </select>
            <span class="field-hint">Kouja provisions remote volumes over its agent API.</span>
          </label>
          {driver === "kouja" ? (
            <>
              <label class="field">
                <span class="field-label">Endpoint</span>
                <input type="text" required autocomplete="off" spellcheck={false} placeholder="10.10.0.1:9443" value={endpoint} onInput={(event) => setEndpoint(event.currentTarget.value)} />
              </label>
              <label class="field">
                <span class="field-label">Protocol</span>
                <select value={protocol} onChange={(event) => setProtocol(event.currentTarget.value)}>
                  <option value="iscsi">iSCSI</option>
                  <option value="nvme-tcp" disabled>NVMe/TCP (coming soon)</option>
                </select>
              </label>
              <label class="field">
                <span class="field-label">Storage class</span>
                <input type="text" required autocomplete="off" spellcheck={false} placeholder="nvme" value={storageClass} onInput={(event) => setStorageClass(event.currentTarget.value)} />
              </label>
              <label class="field">
                <span class="field-label">iSCSI initiator IQN</span>
                <input type="text" required autocomplete="off" spellcheck={false} placeholder="iqn.2026-11.io.example:rookery" value={initiator} onInput={(event) => setInitiator(event.currentTarget.value)} />
              </label>
              <label class="field">
                <span class="field-label">NVMe host NQN <em>optional</em></span>
                <input type="text" autocomplete="off" spellcheck={false} placeholder="nqn.2026-11.io.example:rookery" value={hostNQN} onInput={(event) => setHostNQN(event.currentTarget.value)} />
              </label>
              <label class="field">
                <span class="field-label">CA file</span>
                <input type="text" required autocomplete="off" spellcheck={false} placeholder="/etc/rookery/kouja/ca.crt" value={caFile} onInput={(event) => setCAFile(event.currentTarget.value)} />
              </label>
              <label class="field">
                <span class="field-label">Client certificate</span>
                <input type="text" required autocomplete="off" spellcheck={false} placeholder="/etc/rookery/kouja/rookery.crt" value={clientCertFile} onInput={(event) => setClientCertFile(event.currentTarget.value)} />
              </label>
              <label class="field">
                <span class="field-label">Client key</span>
                <input type="text" required autocomplete="off" spellcheck={false} placeholder="/etc/rookery/kouja/rookery.key" value={clientKeyFile} onInput={(event) => setClientKeyFile(event.currentTarget.value)} />
              </label>
              <label class="field">
                <span class="field-label">TLS server name <em>optional</em></span>
                <input type="text" autocomplete="off" spellcheck={false} placeholder="kouja-storage-1" value={serverName} onInput={(event) => setServerName(event.currentTarget.value)} />
              </label>
              <label class="field">
                <span class="field-label">iSCSI portal <em>optional</em></span>
                <input type="text" autocomplete="off" spellcheck={false} placeholder="10.10.0.1:3260" value={portal} onInput={(event) => setPortal(event.currentTarget.value)} />
              </label>
            </>
          ) : null}
          <label class="field" hidden={driver === "kouja"}>
            <span class="field-label">Volume group</span>
            <input
              type="text"
              required={driver !== "kouja"}
              autocomplete="off"
              spellcheck={false}
              placeholder="vg0"
              value={vg}
              onInput={(event) => setVG(event.currentTarget.value)}
            />
          </label>
          <label class="field" hidden={driver === "kouja"}>
            <span class="field-label">Thin pool</span>
            <input
              type="text"
              required={driver !== "kouja"}
              autocomplete="off"
              spellcheck={false}
              placeholder="rookery"
              value={thinPool}
              onInput={(event) => setThinPool(event.currentTarget.value)}
            />
          </label>
          <label class="field field-wide checkbox-field" hidden={driver === "kouja"}>
            <span class="field-label">Templates</span>
            <span class="checkbox-row">
              <input
                type="checkbox"
                checked={supportsTemplates}
                onChange={(event) => setSupportsTemplates(event.currentTarget.checked)}
              />
              <span>Support template disks and template-based disk creation</span>
            </span>
            <span class="field-hint">
              Clone existing template disks with the backend preparation script.
            </span>
          </label>
          {supportsTemplates && driver !== "kouja" ? (
            <>
              <label class="field field-wide">
                <span class="field-label">Preparation script</span>
                <input
                  type="text"
                  required
                  autocomplete="off"
                  spellcheck={false}
                  placeholder="/usr/local/sbin/rookery-disk-prepare"
                  value={preparationScript}
                  onInput={(event) => setPreparationScript(event.currentTarget.value)}
                />
                <span class="field-hint">
                  Absolute path. Runs as the API user with disk details in ROOKERY_DISK_JSON.
                </span>
              </label>
              <label class="field">
                <span class="field-label">Script timeout</span>
                <input
                  type="text"
                  required
                  autocomplete="off"
                  spellcheck={false}
                  placeholder="5m"
                  value={preparationTimeout}
                  onInput={(event) => setPreparationTimeout(event.currentTarget.value)}
                />
              </label>
              <label class="field field-wide checkbox-field">
                <span class="checkbox-row">
                  <input
                    type="checkbox"
                    checked={allowTemplateDelete}
                    onChange={(event) => setAllowTemplateDelete(event.currentTarget.checked)}
                  />
                  <span>Allow deleting a template while clones still exist</span>
                </span>
              </label>
            </>
          ) : null}
          <div class="field field-wide">
            <LabelsInput labels={labels} onChange={setLabels} disabled={saving} />
          </div>
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
            {saving ? "Saving…" : "Save backend"}
          </button>
        </footer>
      </form>
    </dialog>
  );
}
