import { useEffect, useState } from "preact/hooks";
import { api } from "../../shared/api";
import { showToast } from "../../shared/toast";
import type { Media, VM } from "../types";
import { MediaDialog } from "./MediaDialog";

function formatBytes(bytes: number): string {
  const value = Number(bytes) || 0;
  if (value >= 1024 ** 3) {
    return `${(value / 1024 ** 3).toFixed(value % 1024 ** 3 === 0 ? 0 : 1)} GB`;
  }
  if (value >= 1024 ** 2) {
    return `${(value / 1024 ** 2).toFixed(value % 1024 ** 2 === 0 ? 0 : 1)} MB`;
  }
  if (value >= 1024) {
    return `${(value / 1024).toFixed(value % 1024 === 0 ? 0 : 1)} KB`;
  }
  return `${value} B`;
}

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

function usersFor(mediaID: number, vms: VM[]): VM[] {
  return vms.filter((vm) => vm.boot_media_id === mediaID);
}

export function MediaTab() {
  const [media, setMedia] = useState<Media[]>([]);
  const [vms, setVms] = useState<VM[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [dialogOpen, setDialogOpen] = useState(false);

  const publishMedia = (list: Media[]) => {
    setMedia(list);
    window.dispatchEvent(new CustomEvent("rookery:media-changed", { detail: list }));
  };

  const loadMedia = async (showErrors = true) => {
    try {
      const data = await api<Media[]>("/api/media");
      publishMedia(Array.isArray(data) ? data : []);
      if (showErrors) {
        setError("");
      }
    } catch (err) {
      if (showErrors) {
        setError(err instanceof Error ? err.message : String(err));
      }
    }
  };

  const loadVMs = async () => {
    try {
      const data = await api<VM[]>("/api/vms");
      setVms(Array.isArray(data) ? data : []);
    } catch {
      setVms([]);
    }
  };

  const refreshAll = async () => {
    setLoading(true);
    await Promise.allSettled([loadMedia(), loadVMs()]);
    setLoading(false);
  };

  useEffect(() => {
    void refreshAll();

    const onMediaData = (event: Event) => {
      const detail = (event as CustomEvent<Media[]>).detail;
      if (Array.isArray(detail)) {
        setMedia(detail);
      }
    };
    const onAddMedia = () => {
      setDialogOpen(true);
    };
    const onTabChanged = (event: Event) => {
      if ((event as CustomEvent<string>).detail === "media") {
        void refreshAll();
      }
    };

    window.addEventListener("rookery:media-data", onMediaData);
    window.addEventListener("rookery:add-media", onAddMedia);
    window.addEventListener("rookery:tab-changed", onTabChanged);

    return () => {
      window.removeEventListener("rookery:media-data", onMediaData);
      window.removeEventListener("rookery:add-media", onAddMedia);
      window.removeEventListener("rookery:tab-changed", onTabChanged);
    };
    // The listeners use only state setters and stable API helpers.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    const count = document.getElementById("media-tab-count");
    if (count) {
      count.textContent = String(media.length);
    }
  }, [media.length]);

  const removeMedia = async (item: Media) => {
    const users = usersFor(item.id, vms);
    if (users.length > 0) {
      showToast(`Clear boot media on ${users.map((vm) => vm.name).join(", ")} first.`, true);
      return;
    }

    const confirmed = window.confirm(
      `Remove media "${item.name}"?\n\nThis removes the inventory entry only. The ISO file is not deleted.`
    );
    if (!confirmed) {
      return;
    }

    try {
      await api(`/api/media/${item.id}`, { method: "DELETE" });
      publishMedia(media.filter((entry) => entry.id !== item.id));
      setError("");
      showToast(`Removed media ${item.name}`);
    } catch (err) {
      setError(`Could not remove ${item.name}: ${err instanceof Error ? err.message : String(err)}`);
    }
  };

  const used = media.filter((item) => usersFor(item.id, vms).length > 0).length;
  const summary =
    media.length === 0
      ? loading
        ? "Loading media…"
        : "No installation media registered"
      : `${media.length} media file${media.length === 1 ? "" : "s"} · ${used} in use`;

  return (
    <>
      <section class="panel" aria-labelledby="media-heading">
        <div class="panel-head">
          <div>
            <h2 id="media-heading">Installation media</h2>
            <p class="panel-subtitle">{summary}</p>
          </div>
          <div class="actions">
            <button class="button ghost" type="button" disabled={loading} onClick={() => void refreshAll()}>
              {loading ? "Refreshing…" : "Refresh"}
            </button>
          </div>
        </div>

        {error ? (
          <div class="banner" role="alert">
            {error}
          </div>
        ) : null}

        <div class="table-wrap">
          {media.length > 0 ? (
            <table>
              <thead>
                <tr>
                  <th scope="col">Name</th>
                  <th scope="col">Size</th>
                  <th scope="col">Used as boot media</th>
                  <th scope="col" class="hide-sm">
                    Updated
                  </th>
                  <th scope="col">
                    <span class="sr-only">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {media.map((item) => {
                  const users = usersFor(item.id, vms);
                  const usedLabel =
                    users.length === 0
                      ? "not used"
                      : users.length === 1
                        ? users[0].name
                        : `${users[0].name} +${users.length - 1}`;
                  return (
                    <tr key={item.id}>
                      <td>
                        <span class="vm-name">{item.name}</span>
                        {item.path ? (
                          <span class="vm-description" title={item.path}>
                            {item.path}
                          </span>
                        ) : null}
                      </td>
                      <td class="mono">{formatBytes(item.size_bytes)}</td>
                      <td
                        class={users.length ? "mono" : "mono muted"}
                        title={users.length ? users.map((vm) => vm.name).join(", ") : undefined}
                      >
                        {usedLabel}
                      </td>
                      <td class="updated hide-sm">{formatDate(item.updated_at)}</td>
                      <td>
                        <div class="actions">
                          <button
                            class="row-action danger"
                            type="button"
                            disabled={users.length > 0}
                            title={
                              users.length > 0
                                ? `Clear boot media on ${users.map((vm) => vm.name).join(", ")} first.`
                                : ""
                            }
                            onClick={() => void removeMedia(item)}
                          >
                            Remove
                          </button>
                        </div>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          ) : (
            <div class="empty">
              <div class="empty-mark" aria-hidden="true">
                ◎
              </div>
              <h3>No installation media yet</h3>
              <p>Register a read-only ISO file from the host, then choose it as a VM's boot media.</p>
              <button class="button primary" type="button" onClick={() => setDialogOpen(true)}>
                Add media
              </button>
            </div>
          )}
        </div>
      </section>

      {dialogOpen ? (
        <MediaDialog
          onClose={() => setDialogOpen(false)}
          onSaved={(item) => {
            setDialogOpen(false);
            setError("");
            showToast(`Added media ${item.name}`);
            void loadMedia();
          }}
        />
      ) : null}
    </>
  );
}
