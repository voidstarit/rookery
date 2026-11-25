import { useEffect, useRef, useState } from "preact/hooks";
import { api } from "../../shared/api";
import { showToast } from "../../shared/toast";
import type { LightVMGroup, LightVMGroupMember, LightVMTemplate } from "../types";
import { navigate, useRoute } from "../router";

function formatDate(value: string | undefined): string {
  if (!value) {
    return "—";
  }
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "—" : date.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}

type GroupDialogProps = {
  templates: LightVMTemplate[];
  initialVersionID: number | null;
  onClose: () => void;
  onCreated: (group: LightVMGroup) => void;
};

function GroupDialog({ templates, initialVersionID, onClose, onCreated }: GroupDialogProps) {
  const dialogRef = useRef<HTMLDialogElement | null>(null);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [versionID, setVersionID] = useState(
    initialVersionID && templates.some((item) => item.latest_version_id === initialVersionID)
      ? String(initialVersionID)
      : templates[0]
        ? String(templates[0].latest_version_id)
        : ""
  );
  const [desiredCount, setDesiredCount] = useState("1");
  const [paused, setPaused] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    dialogRef.current?.showModal();
  }, []);

  const close = () => {
    if (!saving) {
      dialogRef.current?.close();
      onClose();
    }
  };

  const submit = async (event: Event) => {
    event.preventDefault();
    setSaving(true);
    setError("");
    try {
      const group = await api<LightVMGroup>("/api/lightvm/groups", {
        method: "POST",
        body: JSON.stringify({
          name: name.trim(),
          description: description.trim(),
          template_version_id: Number(versionID),
          desired_count: Number(desiredCount),
          paused,
        }),
      });
      showToast(`Created group ${group.name}`);
      onCreated(group);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  };

  return (
    <dialog
      ref={dialogRef}
      id="lightvm-group-dialog"
      aria-labelledby="lightvm-group-dialog-title"
      onClose={onClose}
      onCancel={(event) => {
        if (saving) {
          event.preventDefault();
        }
      }}
      onClick={(event) => {
        if (event.target === event.currentTarget) {
          close();
        }
      }}
    >
      <form onSubmit={(event) => void submit(event)}>
        <header class="dialog-head">
          <div>
            <p class="eyebrow">Managed fleet</p>
            <h2 id="lightvm-group-dialog-title">Create LightVM group</h2>
          </div>
          <button class="icon-button" type="button" aria-label="Close dialog" onClick={close}>×</button>
        </header>

        {error ? <div class="banner" role="alert">{error}</div> : null}

        <fieldset class="form-grid dialog-form-fieldset" disabled={saving}>
          <label class="field field-wide">
            <span class="field-label">Name</span>
            <input required maxlength={64} value={name} onInput={(event) => setName(event.currentTarget.value)} />
          </label>
          <label class="field field-wide">
            <span class="field-label">Description</span>
            <input maxlength={2000} value={description} onInput={(event) => setDescription(event.currentTarget.value)} />
          </label>
          <label class="field field-wide">
            <span class="field-label">Template version</span>
            <select required value={versionID} onChange={(event) => setVersionID(event.currentTarget.value)}>
              {templates.length === 0 ? <option value="">Create a template first</option> : null}
              {templates.map((template) => (
                <option value={String(template.latest_version_id)} key={template.id}>
                  {template.name} v{template.latest_version}
                </option>
              ))}
            </select>
            <span class="field-hint">Members created later use this version. Existing members keep theirs.</span>
          </label>
          <label class="field">
            <span class="field-label">Desired count</span>
            <input
              type="number"
              min={0}
              max={1024}
              required
              value={desiredCount}
              onInput={(event) => setDesiredCount(event.currentTarget.value)}
            />
          </label>
          <label class="field">
            <span class="field-label">Paused</span>
            <label class="checkbox">
              <input type="checkbox" checked={paused} onChange={(event) => setPaused(event.currentTarget.checked)} />
              Suppress creates and restarts
            </label>
          </label>
        </fieldset>

        <div class="dialog-actions">
          <button class="button ghost" type="button" disabled={saving} onClick={close}>Cancel</button>
          <button class="button primary" type="submit" disabled={saving || templates.length === 0}>
            {saving ? "Creating…" : "Create group"}
          </button>
        </div>
      </form>
    </dialog>
  );
}

function GroupDetail({ groupID, onChanged }: { groupID: number; onChanged: () => void }) {
  const [group, setGroup] = useState<LightVMGroup | null>(null);
  const [desiredCount, setDesiredCount] = useState("0");
  const [error, setError] = useState("");
  const [action, setAction] = useState("");

  const load = async (showErrors = true) => {
    try {
      const data = await api<LightVMGroup>(`/api/lightvm/groups/${groupID}`);
      setGroup(data);
      setDesiredCount(String(data.desired_count));
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
    void load();
    const timer = window.setInterval(() => void load(false), 5000);
    return () => window.clearInterval(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [groupID]);

  if (!group) {
    return (
      <section class="panel panel-spaced">
        {error ? <div class="banner" role="alert">{error}</div> : <p class="panel-subtitle">Loading group…</p>}
      </section>
    );
  }

  const update = async (body: Record<string, unknown>, message: string) => {
    setAction(message);
    setError("");
    try {
      await api(`/api/lightvm/groups/${groupID}`, { method: "PUT", body: JSON.stringify(body) });
      showToast(message);
      onChanged();
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setAction("");
    }
  };

  const remove = async (force: boolean) => {
    const text = force
      ? `Force-delete group "${group.name}"? This removes every member and owned resource; an undeletable stopped LightVM may remain as inventory.`
      : `Delete group "${group.name}"?`;
    if (!window.confirm(text)) {
      return;
    }
    setAction(force ? "force-delete" : "delete");
    setError("");
    try {
      await api(`/api/lightvm/groups/${groupID}${force ? "?force=true" : ""}`, { method: "DELETE" });
      showToast(force ? `Force-deleted ${group.name}` : `Deleted ${group.name}`);
      onChanged();
      navigate("/lightvm/groups");
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setAction("");
    }
  };

  const memberAction = async (member: LightVMGroupMember, actionName: "start" | "stop") => {
    setAction(`${actionName}:${member.id}`);
    setError("");
    try {
      await api(`/api/lightvm/groups/${groupID}/members/${member.id}/${actionName}`, { method: "POST" });
      showToast(`${actionName === "start" ? "Starting" : "Stopping"} ${member.name}`);
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setAction("");
    }
  };

  return (
    <section class="panel panel-spaced" aria-labelledby="lightvm-group-detail-heading">
      <div class="panel-head">
        <div>
          <p class="eyebrow">LightVM group</p>
          <h2 id="lightvm-group-detail-heading">{group.name}</h2>
          <p class="panel-subtitle">
            {group.template_name} v{group.template_version} · {group.state}
            {group.paused ? " · paused" : ""}
          </p>
        </div>
        <div class="actions">
          <button class="button ghost" type="button" onClick={() => navigate("/lightvm/groups")}>Back</button>
          <button
            class="button ghost"
            type="button"
            disabled={Boolean(action)}
            onClick={() => void update({ paused: !group.paused }, group.paused ? "Group resumed" : "Group paused")}
          >
            {group.paused ? "Resume" : "Pause"}
          </button>
          <button class="button ghost danger" type="button" disabled={Boolean(action)} onClick={() => void remove(false)}>
            Delete
          </button>
          <button class="button ghost danger" type="button" disabled={Boolean(action)} onClick={() => void remove(true)}>
            Force delete
          </button>
        </div>
      </div>

      {error ? <div class="banner" role="alert">{error}</div> : null}
      {group.last_error ? <div class="banner" role="alert">Last reconcile error: {group.last_error}</div> : null}

      <div class="count-chips">
        <span class="count-chip">desired <strong>{group.desired_count}</strong></span>
        <span class="count-chip">actual <strong>{group.actual_count}</strong></span>
        <span class="count-chip">running <strong>{group.running_count}</strong></span>
        <span class="count-chip">provisioning <strong>{group.provisioning_count}</strong></span>
        <span class="count-chip">removing <strong>{group.removing_count}</strong></span>
        <span class="count-chip">drift <strong>{group.drift}</strong></span>
        <span class={`count-chip ${group.converged ? "good" : "warn"}`}>
          {group.converged ? "converged" : "reconciling"}
        </span>
      </div>

      <div class="form-grid compact-form">
        <label class="field">
          <span class="field-label">Desired count</span>
          <input
            type="number"
            min={0}
            max={1024}
            value={desiredCount}
            onInput={(event) => setDesiredCount(event.currentTarget.value)}
          />
        </label>
        <div class="field">
          <span class="field-label">&nbsp;</span>
          <button
            class="button primary"
            type="button"
            disabled={Boolean(action) || desiredCount === String(group.desired_count)}
            onClick={() => void update({ desired_count: Number(desiredCount) }, "Desired count updated")}
          >
            Save count
          </button>
        </div>
      </div>

      <p class="field-hint">
        Last reconcile: {formatDate(group.last_reconcile_at)}. Ready is running/desired; converged means
        no provisioning or removing members remain.
      </p>

      <div class="table-wrap">
        {group.members && group.members.length > 0 ? (
          <table>
            <thead>
              <tr>
                <th scope="col">Member</th>
                <th scope="col">State</th>
                <th scope="col">Status</th>
                <th scope="col">Version</th>
                <th scope="col" class="hide-sm">Next retry</th>
                <th scope="col"><span class="sr-only">Actions</span></th>
              </tr>
            </thead>
            <tbody>
              {group.members.map((member) => (
                <tr key={member.id}>
                  <td>
                    <span class="vm-name mono">{member.name}</span>
                    {member.workload_id ? <span class="vm-description mono">{member.workload_id}</span> : null}
                    {member.last_error ? <span class="vm-description error-text">{member.last_error}</span> : null}
                  </td>
                  <td class="mono">{member.state}</td>
                  <td><span class="status-badge" data-status={member.status}>{member.status}</span></td>
                  <td class="mono">v{member.template_version}</td>
                  <td class="updated hide-sm">{formatDate(member.next_retry_at)}</td>
                  <td>
                    <div class="actions">
                      <button
                        class="row-action"
                        type="button"
                        disabled={Boolean(action)}
                        onClick={() => void memberAction(member, "start")}
                      >
                        Start
                      </button>
                      <button
                        class="row-action danger"
                        type="button"
                        disabled={Boolean(action)}
                        onClick={() => void memberAction(member, "stop")}
                      >
                        Stop
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
            <h3>No members yet</h3>
            <p>The fleet worker will create members on the next reconcile pass.</p>
          </div>
        )}
      </div>
    </section>
  );
}

export function GroupsTab() {
  const [groups, setGroups] = useState<LightVMGroup[]>([]);
  const [templates, setTemplates] = useState<LightVMTemplate[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const route = useRoute();
  const groupID = route.segments[1] === "groups" && route.segments[2] && route.segments[2] !== "new"
    ? Number(route.segments[2])
    : null;
  const creating = route.segments[1] === "groups" && route.segments[2] === "new";

  const load = async (showErrors = true) => {
    try {
      const [groupData, templateData] = await Promise.all([
        api<LightVMGroup[]>("/api/lightvm/groups"),
        api<LightVMTemplate[]>("/api/lightvm/templates"),
      ]);
      const list = Array.isArray(groupData) ? groupData : [];
      setGroups(list);
      setTemplates(Array.isArray(templateData) ? templateData : []);
      window.dispatchEvent(new CustomEvent("rookery:lightvm-groups-changed", { detail: list }));
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

  if (groupID && Number.isFinite(groupID)) {
    return <GroupDetail groupID={groupID} onChanged={() => void load(false)} />;
  }

  const removeGroup = async (group: LightVMGroup) => {
    if (!window.confirm(`Delete group "${group.name}"?`)) {
      return;
    }
    try {
      await api(`/api/lightvm/groups/${group.id}`, { method: "DELETE" });
      showToast(`Deleted group ${group.name}`);
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  const summary = loading
    ? "Loading groups…"
    : groups.length === 0
      ? "No LightVM groups defined"
      : `${groups.length} group${groups.length === 1 ? "" : "s"}`;

  return (
    <section class="panel panel-spaced" aria-labelledby="lightvm-groups-heading">
      <div class="panel-head">
        <div>
          <h2 id="lightvm-groups-heading">LightVM groups</h2>
          <p class="panel-subtitle">{summary}</p>
        </div>
        <div class="actions">
          <button class="button ghost" type="button" disabled={loading} onClick={() => void load()}>
            {loading ? "Refreshing…" : "Refresh"}
          </button>
          <button class="button primary" type="button" onClick={() => navigate("/lightvm/groups/new")}>
            <span class="button-plus" aria-hidden="true">+</span>
            New group
          </button>
        </div>
      </div>

      <p class="field-hint">
        A group maintains a desired number of members from a pinned template version. Members are
        repaired with fresh disks and interfaces; explicit stops are preserved.
      </p>

      {error ? <div class="banner" role="alert">{error}</div> : null}

      <div class="table-wrap">
        {groups.length > 0 ? (
          <table>
            <thead>
              <tr>
                <th scope="col">Group</th>
                <th scope="col">Template</th>
                <th scope="col">Desired / actual</th>
                <th scope="col">Running</th>
                <th scope="col">Drift</th>
                <th scope="col">State</th>
                <th scope="col"><span class="sr-only">Actions</span></th>
              </tr>
            </thead>
            <tbody>
              {groups.map((group) => (
                <tr key={group.id}>
                  <td>
                    <span class="vm-name">{group.name}</span>
                    {group.description ? <span class="vm-description">{group.description}</span> : null}
                  </td>
                  <td class="mono">{group.template_name} v{group.template_version}</td>
                  <td class="mono">{group.desired_count} / {group.actual_count}</td>
                  <td class="mono">{group.running_count}</td>
                  <td class="mono">{group.drift}</td>
                  <td>
                    <span class={`count-chip ${group.converged ? "good" : "warn"}`}>
                      {group.paused ? "paused" : group.converged ? "converged" : "reconciling"}
                    </span>
                  </td>
                  <td>
                    <div class="actions">
                      <button class="row-action" type="button" onClick={() => navigate(`/lightvm/groups/${group.id}`)}>
                        Open
                      </button>
                      <button class="row-action danger" type="button" onClick={() => void removeGroup(group)}>
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
            <h3>No LightVM groups yet</h3>
            <p>Create a template, then create a group to maintain a fleet of identical LightVMs.</p>
            <button class="button primary" type="button" onClick={() => navigate("/lightvm/groups/new")}>
              New group
            </button>
          </div>
        )}
      </div>

      {creating ? (
        <GroupDialog
          templates={templates}
          initialVersionID={Number(route.query.get("template_version_id")) || null}
          onClose={() => navigate("/lightvm/groups")}
          onCreated={(group) => {
            void load();
            navigate(`/lightvm/groups/${group.id}`);
          }}
        />
      ) : null}
    </section>
  );
}
