import { useEffect, useState } from "preact/hooks";
import { api } from "../../shared/api";
import { showToast } from "../../shared/toast";
import type {
  LightVMInitramfs,
  LightVMKernel,
  LightVMTemplate,
  Network,
  StorageBackend,
} from "../types";
import { navigate, useRoute } from "../router";
import { TemplateEditorDialog } from "./TemplateEditorDialog";

type Props = {
  kernels: LightVMKernel[];
  initramfs: LightVMInitramfs[];
};

function formatDate(value: string | undefined): string {
  if (!value) {
    return "—";
  }
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "—" : date.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}

export function TemplatesTab({ kernels, initramfs }: Props) {
  const [templates, setTemplates] = useState<LightVMTemplate[]>([]);
  const [backends, setBackends] = useState<StorageBackend[]>([]);
  const [networks, setNetworks] = useState<Network[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [editor, setEditor] = useState<
    | { mode: "create" }
    | { mode: "version"; template: LightVMTemplate }
    | { mode: "metadata"; template: LightVMTemplate }
    | null
  >(null);
  const route = useRoute();

  const load = async (showErrors = true) => {
    try {
      const [templateData, backendData, networkData] = await Promise.all([
        api<LightVMTemplate[]>("/api/lightvm/templates"),
        api<StorageBackend[]>("/api/storage-backends").catch(() => []),
        api<Network[]>("/api/networks").catch(() => []),
      ]);
      const list = Array.isArray(templateData) ? templateData : [];
      setTemplates(list);
      setBackends(Array.isArray(backendData) ? backendData : []);
      setNetworks(Array.isArray(networkData) ? networkData : []);
      window.dispatchEvent(new CustomEvent("rookery:lightvm-templates-changed", { detail: list }));
      if (showErrors) {
        setError("");
      }
    } catch (err) {
      if (showErrors) {
        setError(err instanceof Error ? err.message : String(err));
      }
    }
  };

  useEffect(() => {
    setLoading(true);
    void load().finally(() => setLoading(false));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (route.segments[1] !== "templates") {
      setEditor(null);
      return;
    }
    if (route.segments[2] === "new") {
      setEditor({ mode: "create" });
    } else {
      setEditor(null);
    }
  }, [route.path, route.segments]);

  const removeTemplate = async (template: LightVMTemplate) => {
    if (!window.confirm(`Delete LightVM template "${template.name}"?\n\nA template pinned by a group cannot be deleted.`)) {
      return;
    }
    try {
      await api(`/api/lightvm/templates/${template.id}`, { method: "DELETE" });
      showToast(`Deleted template ${template.name}`);
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  const summary = loading
    ? "Loading templates…"
    : templates.length === 0
      ? "No LightVM templates defined"
      : `${templates.length} template${templates.length === 1 ? "" : "s"}`;

  return (
    <section class="panel panel-spaced" aria-labelledby="lightvm-templates-heading">
      <div class="panel-head">
        <div>
          <h2 id="lightvm-templates-heading">LightVM templates</h2>
          <p class="panel-subtitle">{summary}</p>
        </div>
        <div class="actions">
          <button class="button ghost" type="button" disabled={loading} onClick={() => void load()}>
            {loading ? "Refreshing…" : "Refresh"}
          </button>
          <button class="button primary" type="button" onClick={() => navigate("/lightvm/templates/new")}>
            <span class="button-plus" aria-hidden="true">+</span>
            New template
          </button>
        </div>
      </div>

      <p class="field-hint">
        Templates pin the image, compute, data disks, and network interfaces used to create group
        members. Editing a specification creates a new immutable version.
      </p>

      {error ? <div class="banner" role="alert">{error}</div> : null}

      <div class="table-wrap">
        {templates.length > 0 ? (
          <table>
            <thead>
              <tr>
                <th scope="col">Name</th>
                <th scope="col">Latest version</th>
                <th scope="col">Versions</th>
                <th scope="col" class="hide-sm">Updated</th>
                <th scope="col"><span class="sr-only">Actions</span></th>
              </tr>
            </thead>
            <tbody>
              {templates.map((template) => (
                <tr key={template.id}>
                  <td>
                    <span class="vm-name">{template.name}</span>
                    {template.description ? <span class="vm-description">{template.description}</span> : null}
                  </td>
                  <td class="mono">v{template.latest_version}</td>
                  <td class="mono">{template.version_count}</td>
                  <td class="updated hide-sm">{formatDate(template.updated_at)}</td>
                  <td>
                    <div class="actions">
                      <button class="row-action" type="button" onClick={() => setEditor({ mode: "version", template })}>
                        New version
                      </button>
                      <button
                        class="row-action"
                        type="button"
                        onClick={() => navigate(`/lightvm/groups/new?template_version_id=${template.latest_version_id}`)}
                      >
                        New group
                      </button>
                      <button class="row-action" type="button" onClick={() => setEditor({ mode: "metadata", template })}>
                        Edit
                      </button>
                      <button class="row-action danger" type="button" onClick={() => void removeTemplate(template)}>
                        Delete
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : (
          <div class="empty">
            <div class="empty-mark" aria-hidden="true">◇</div>
            <h3>No LightVM templates yet</h3>
            <p>Create a template to define a repeatable image-backed LightVM fleet.</p>
            <button class="button primary" type="button" onClick={() => navigate("/lightvm/templates/new")}>
              New template
            </button>
          </div>
        )}
      </div>

      {editor ? (
        <TemplateEditorDialog
          mode={editor.mode}
          template={editor.mode === "create" ? null : editor.template}
          kernels={kernels}
          initramfs={initramfs}
          backends={backends}
          networks={networks}
          onClose={() => {
            setEditor(null);
            if (route.segments[2] === "new") {
              navigate("/lightvm/templates");
            }
          }}
          onSaved={() => {
            setEditor(null);
            void load();
            navigate("/lightvm/templates");
          }}
        />
      ) : null}
    </section>
  );
}
