import { useEffect, useState } from "preact/hooks";
import { api } from "../../shared/api";
import { showToast } from "../../shared/toast";
import type { StartupScript } from "../types";
import { CgroupSettingsPanel } from "./CgroupSettingsPanel";

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

function badgeStatus(status: string | undefined): string {
  switch (status) {
    case "success":
      return "running";
    case "failed":
      return "error";
    case "running":
      return "starting";
    case "pending":
      return "paused";
    default:
      return "disabled";
  }
}

type ScriptKind = "rookery" | "vpp";

type ScriptSectionProps = {
  kind: ScriptKind;
  title: string;
  endpoint: string;
  description: string;
  placeholder: string;
  spaced?: boolean;
};

function ScriptSection({ kind, title, endpoint, description, placeholder, spaced }: ScriptSectionProps) {
  const [current, setCurrent] = useState<StartupScript | null>(null);
  const [enabled, setEnabled] = useState(false);
  const [script, setScript] = useState("");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const label = kind === "rookery" ? "Rookery" : "VPP";
  const headingID = `${kind}-startup-script-heading`;

  const load = async () => {
    setLoading(true);
    try {
      const data = await api<StartupScript>(endpoint);
      setCurrent(data);
      setEnabled(Boolean(data.enabled));
      setScript(data.script || "");
      setError("");
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void load();
  }, []);

  const save = async (event: Event) => {
    event.preventDefault();
    if (saving) {
      return;
    }
    if (enabled && !script.trim()) {
      setError(`Enter a ${label} script before enabling it.`);
      return;
    }
    setSaving(true);
    setError("");
    try {
      const updated = await api<StartupScript>(endpoint, {
        method: "PUT",
        body: JSON.stringify({ enabled, script }),
      });
      setCurrent(updated);
      setEnabled(Boolean(updated.enabled));
      setScript(updated.script || "");
      showToast(
        updated.enabled
          ? `Saved ${label} startup script${kind === "rookery" ? "; it will run on the next API start" : "; it will run on the next VPP connection"}`
          : `Saved ${label} startup script settings`
      );
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  };

  const status = current?.status || "disabled";
  const output = current?.output || "";
  const exitCode = status === "success" ? 0 : (current?.exit_code ?? "—");

  return (
    <section class={spaced ? "panel panel-spaced" : "panel"} aria-labelledby={headingID}>
      <div class="panel-head">
        <div>
          <h2 id={headingID}>{title}</h2>
          <p class="panel-subtitle">
            {kind === "rookery" ? "Rookery server lifecycle hook" : "Applied whenever Rookery connects to FD.io VPP"}
          </p>
        </div>
        <button class="button ghost" type="button" disabled={loading || saving} onClick={() => void load()}>
          {loading ? "Refreshing…" : "Refresh"}
        </button>
      </div>

      <p class="field-hint">{description}</p>

      <div class="startup-script-status">
        <span class="status-badge" data-status={badgeStatus(status)}>
          {status}
        </span>
        {status === "success" || status === "failed" ? (
          <span class="field-hint">
            exit code {exitCode}
            {current?.ran_at ? ` · ${formatDate(current.ran_at)}` : ""}
          </span>
        ) : status === "running" ? (
          <span class="field-hint">Running…</span>
        ) : enabled ? (
          <span class="field-hint">
            {kind === "rookery"
              ? "Runs on the next API start."
              : "Runs on the next VPP connection."}
          </span>
        ) : (
          <span class="field-hint">Disabled.</span>
        )}
      </div>

      <form class="form-grid" onSubmit={(event) => void save(event)}>
        <label class="field field-wide checkbox-field">
          <span class="checkbox-row">
            <input
              type="checkbox"
              checked={enabled}
              onChange={(event) => setEnabled(event.currentTarget.checked)}
            />
            <span>Enable {label} startup script</span>
          </span>
        </label>

        <label class="field field-wide">
          <span class="field-label">Script</span>
          <textarea
            class="startup-script-editor"
            rows={kind === "rookery" ? 7 : 9}
            spellcheck={false}
            placeholder={placeholder}
            value={script}
            onInput={(event) => setScript(event.currentTarget.value)}
          />
          <span class="field-hint">
            The script runs with `/bin/sh` and the API process privileges. Only configure trusted
            commands.
          </span>
        </label>

        {error ? (
          <p class="form-error field-wide" role="alert">
            {error}
          </p>
        ) : null}

        <div class="field-wide actions">
          <button class="button primary" type="submit" disabled={saving || loading}>
            {saving ? "Saving…" : `Save ${label} startup script`}
          </button>
        </div>
      </form>

      {output ? (
        <details class="startup-script-output" open={status === "failed"}>
          <summary>Last output</summary>
          <pre>{output}</pre>
        </details>
      ) : null}
    </section>
  );
}

export function SettingsTab() {
  return (
    <>
      <CgroupSettingsPanel />
      <ScriptSection
        kind="rookery"
        title="Rookery startup script"
        endpoint="/api/startup-script"
        description="Runs once per API process after Rookery starts. It is empty by default. Use it for host-level setup that should happen once when the service starts."
        placeholder={"# Runs once when the Rookery API starts\n# Add host or setup commands here"}
        spaced
      />
      <ScriptSection
        kind="vpp"
        title="VPP startup script"
        endpoint="/api/vpp-startup-script"
        description="Runs every time Rookery connects to FD.io VPP, including after the connection drops or VPP restarts. It is enabled by default to assign NAT44 workers."
        placeholder={"# Assign NAT44 workers\nvppctl set nat workers 0"}
        spaced
      />
    </>
  );
}
