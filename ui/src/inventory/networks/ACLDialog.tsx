import { useEffect, useRef, useState } from "preact/hooks";
import { api } from "../../shared/api";
import type { ACL, ACLRule } from "../types";
import { defaultACLRule, formatACLText } from "./acl";

type ACLDialogProps = {
  acl: ACL | null;
  vppReady: boolean;
  onClose: () => void;
  onSaved: (acl: ACL) => void;
};

const NAME_PATTERN = "[A-Za-z0-9][A-Za-z0-9._-]*";

function normalizeRules(rules: ACLRule[]): ACLRule[] {
  return rules.map((rule) => {
    const portsEnabled = rule.protocol === "tcp" || rule.protocol === "udp";
    return {
      action: rule.action,
      description: rule.description?.trim() || "",
      protocol: rule.protocol,
      source: rule.source.trim(),
      destination: rule.destination.trim(),
      source_port_start: portsEnabled ? Number(rule.source_port_start) || 0 : 0,
      source_port_end: portsEnabled ? Number(rule.source_port_end) || 0 : 0,
      destination_port_start: portsEnabled ? Number(rule.destination_port_start) || 0 : 0,
      destination_port_end: portsEnabled ? Number(rule.destination_port_end) || 0 : 0,
    };
  });
}

export function ACLDialog({ acl, vppReady, onClose, onSaved }: ACLDialogProps) {
  const editing = acl !== null;
  const [name, setName] = useState(acl?.name ?? "");
  const [title, setTitle] = useState(acl?.title ?? "");
  const [description, setDescription] = useState(acl?.description ?? "");
  const [rules, setRules] = useState<ACLRule[]>(() => {
    const initial = acl?.rules?.length ? acl.rules.map((rule) => ({ ...rule })) : [defaultACLRule()];
    return initial;
  });
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

  const updateRule = <K extends keyof ACLRule>(index: number, key: K, value: ACLRule[K]) => {
    setRules((current) =>
      current.map((rule, index_) => (index_ === index ? { ...rule, [key]: value } : rule))
    );
  };

  const removeRule = (index: number) => {
    setRules((current) => current.filter((_, index_) => index_ !== index));
  };

  const submit = async (event: Event) => {
    event.preventDefault();
    if (saving || !vppReady || !formRef.current?.reportValidity()) {
      return;
    }
    if (rules.length === 0) {
      setError("Add at least one rule.");
      return;
    }
    setError("");
    setSaving(true);
    try {
      const payload = {
        name: name.trim(),
        title: title.trim(),
        description: description.trim(),
        rules: normalizeRules(rules),
      };
      const saved = await api<ACL>(editing ? `/api/acls/${acl?.id}` : "/api/acls", {
        method: editing ? "PUT" : "POST",
        body: JSON.stringify(payload),
      });
      onSaved(saved);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  };

  const preview = formatACLText({
    name: name.trim(),
    title: title.trim(),
    description: description.trim(),
    rules: normalizeRules(rules),
  });

  return (
    <dialog
      ref={dialogRef}
      id="acl-dialog"
      aria-labelledby="acl-dialog-title"
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
            <p class="eyebrow">{editing ? "Edit ACL" : "New ACL"}</p>
            <h2 id="acl-dialog-title">{editing ? `ACL · ${acl?.name}` : "Define ACL"}</h2>
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
              placeholder="web-ingress"
              value={name}
              onInput={(event) => setName(event.currentTarget.value)}
            />
          </label>
          <label class="field">
            <span class="field-label">Title</span>
            <input
              type="text"
              maxlength={128}
              autocomplete="off"
              placeholder="Web server ingress"
              value={title}
              onInput={(event) => setTitle(event.currentTarget.value)}
            />
          </label>
          <label class="field field-wide">
            <span class="field-label">Description <em>optional</em></span>
            <textarea
              rows={2}
              maxlength={2000}
              placeholder="What this ACL protects"
              value={description}
              onInput={(event) => setDescription(event.currentTarget.value)}
            />
          </label>
        </div>

        <div class="form-grid">
          <div class="field field-wide">
            <div class="panel-head acl-rules-head">
              <div>
                <span class="field-label">Ordered rules</span>
                <span class="field-hint">First match wins. Traffic that matches no rule is denied.</span>
              </div>
              <button
                class="button ghost"
                type="button"
                onClick={() => setRules((current) => [...current, defaultACLRule()])}
              >
                Add rule
              </button>
            </div>
            <div class="acl-rule-rows">
              {rules.map((rule, index) => {
                const portsEnabled = rule.protocol === "tcp" || rule.protocol === "udp";
                return (
                  <div class="acl-rule" key={index}>
                    <div class="acl-rule-head">
                      <span class="acl-rule-label">Rule {index + 1}</span>
                      <button
                        class="row-action danger"
                        type="button"
                        onClick={() => removeRule(index)}
                      >
                        Remove
                      </button>
                    </div>
                    <div class="acl-rule-fields">
                      <label class="field acl-field-acl-rule-action">
                        <span class="field-label">Action</span>
                        <select
                          value={rule.action}
                          onChange={(event) => updateRule(index, "action", event.currentTarget.value)}
                        >
                          <option value="permit">Permit</option>
                          <option value="deny">Deny</option>
                        </select>
                      </label>
                      <label class="field acl-field-acl-rule-protocol">
                        <span class="field-label">Protocol</span>
                        <select
                          value={rule.protocol}
                          onChange={(event) =>
                            updateRule(index, "protocol", event.currentTarget.value)
                          }
                        >
                          <option value="any">Any</option>
                          <option value="tcp">TCP</option>
                          <option value="udp">UDP</option>
                          <option value="icmp">ICMP</option>
                          <option value="icmpv6">ICMPv6</option>
                        </select>
                      </label>
                      <label class="field acl-field-acl-rule-source">
                        <span class="field-label">Source CIDR</span>
                        <input
                          type="text"
                          autocomplete="off"
                          spellcheck={false}
                          placeholder="0.0.0.0/0"
                          value={rule.source}
                          onInput={(event) => updateRule(index, "source", event.currentTarget.value)}
                        />
                      </label>
                      <label class="field acl-field-acl-rule-destination">
                        <span class="field-label">Destination CIDR</span>
                        <input
                          type="text"
                          autocomplete="off"
                          spellcheck={false}
                          placeholder="0.0.0.0/0"
                          value={rule.destination}
                          onInput={(event) =>
                            updateRule(index, "destination", event.currentTarget.value)
                          }
                        />
                      </label>
                      <label class="field acl-field-acl-rule-description">
                        <span class="field-label">Description (optional)</span>
                        <input
                          type="text"
                          autocomplete="off"
                          spellcheck={false}
                          placeholder="Why this rule exists"
                          value={rule.description || ""}
                          onInput={(event) =>
                            updateRule(index, "description", event.currentTarget.value)
                          }
                        />
                      </label>
                      <label class="field acl-field-acl-rule-source-start">
                        <span class="field-label">Source ports</span>
                        <div class="acl-port-pair">
                          <label class="acl-port-input">
                            <span>Start</span>
                            <input
                              type="number"
                              min={1}
                              max={65535}
                              placeholder="any"
                              disabled={!portsEnabled}
                              value={rule.source_port_start || ""}
                              onInput={(event) =>
                                updateRule(index, "source_port_start", Number(event.currentTarget.value))
                              }
                            />
                          </label>
                          <label class="acl-port-input">
                            <span>End</span>
                            <input
                              type="number"
                              min={1}
                              max={65535}
                              placeholder="any"
                              disabled={!portsEnabled}
                              value={rule.source_port_end || ""}
                              onInput={(event) =>
                                updateRule(index, "source_port_end", Number(event.currentTarget.value))
                              }
                            />
                          </label>
                        </div>
                      </label>
                      <label class="field acl-field-acl-rule-destination-start">
                        <span class="field-label">Destination ports</span>
                        <div class="acl-port-pair">
                          <label class="acl-port-input">
                            <span>Start</span>
                            <input
                              type="number"
                              min={1}
                              max={65535}
                              placeholder="any"
                              disabled={!portsEnabled}
                              value={rule.destination_port_start || ""}
                              onInput={(event) =>
                                updateRule(
                                  index,
                                  "destination_port_start",
                                  Number(event.currentTarget.value)
                                )
                              }
                            />
                          </label>
                          <label class="acl-port-input">
                            <span>End</span>
                            <input
                              type="number"
                              min={1}
                              max={65535}
                              placeholder="any"
                              disabled={!portsEnabled}
                              value={rule.destination_port_end || ""}
                              onInput={(event) =>
                                updateRule(
                                  index,
                                  "destination_port_end",
                                  Number(event.currentTarget.value)
                                )
                              }
                            />
                          </label>
                        </div>
                      </label>
                    </div>
                  </div>
                );
              })}
            </div>
          </div>
        </div>

        <label class="field field-wide">
          <span class="field-label">Full ACL preview</span>
          <pre class="acl-preview">{preview}</pre>
        </label>

        {error ? (
          <p class="form-error" role="alert">
            {error}
          </p>
        ) : null}

        <footer class="dialog-actions">
          <button class="button ghost" type="button" disabled={saving} onClick={close}>
            Cancel
          </button>
          <button
            class="button primary"
            type="submit"
            disabled={saving || !vppReady}
            title={vppReady ? "" : "VPP must be ready to save ACLs."}
          >
            {saving ? "Saving…" : "Save ACL"}
          </button>
        </footer>
      </form>
    </dialog>
  );
}
