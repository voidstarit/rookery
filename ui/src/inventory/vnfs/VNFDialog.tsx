import { useEffect, useRef, useState } from "preact/hooks";
import { api } from "../../shared/api";
import type { DNATRule, HostInterface, Network, VNF, VNFConfig, VNFType } from "../types";

type VNFDialogProps = {
  vnf: VNF | null;
  networks: Network[];
  vppReady: boolean;
  initialNetworkID?: number;
  onClose: () => void;
  onSaved: (vnf: VNF) => void;
};

type FormState = {
  name: string;
  networkID: number;
  type: VNFType;
  enabled: boolean;
  gateway: string;
  dns: string;
  rangeStart: string;
  rangeEnd: string;
  leaseTime: string;
  outsideInterface: string;
  outsideAddress: string;
  outsideGateway: string;
  nat44Pool: string;
  dnatRules: DNATRule[];
};

const NAME_PATTERN = "[A-Za-z0-9][A-Za-z0-9._-]*";

function defaultForm(vnf: VNF | null, networks: Network[], vppReady: boolean, initialNetworkID?: number): FormState {
  const type = vnf?.type ?? "dhcpv4";
  const config = vnf?.config ?? {};
  return {
    name: vnf?.name ?? "",
    networkID: vnf?.network_id ?? initialNetworkID ?? networks[0]?.id ?? 0,
    type,
    enabled: vnf ? vnf.enabled : vppReady,
    gateway: config.gateway ?? (type === "dhcpv6" ? "fd00:20::1/64" : "10.20.0.1/24"),
    dns: config.dns ?? (type === "dhcpv6" ? "2001:4860:4860::8888" : "1.1.1.1"),
    rangeStart: config.range_start ?? (type === "dhcpv6" ? "fd00:20::100" : "10.20.0.100"),
    rangeEnd: config.range_end ?? (type === "dhcpv6" ? "fd00:20::200" : "10.20.0.200"),
    leaseTime: config.lease_time ?? "12h",
    outsideInterface: config.outside_interface ?? "",
    outsideAddress: config.outside_address ?? "",
    outsideGateway: config.outside_gateway ?? "",
    nat44Pool: config.nat44_pool ?? "",
    dnatRules: Array.isArray(config.dnat_rules) ? config.dnat_rules.map((rule) => ({ ...rule })) : [],
  };
}

function applyTypeDefaults(state: FormState, type: VNFType): FormState {
  if (type === "dhcpv6") {
    return {
      ...state,
      type,
      gateway: "fd00:20::1/64",
      dns: "2001:4860:4860::8888",
      rangeStart: "fd00:20::100",
      rangeEnd: "fd00:20::200",
      leaseTime: "12h",
    };
  }
  if (type === "nat44") {
    return {
      ...state,
      type,
      gateway: "10.20.0.1/24",
      outsideAddress: "",
      outsideGateway: "",
      nat44Pool: "",
    };
  }
  return {
    ...state,
    type,
    gateway: "10.20.0.1/24",
    dns: "1.1.1.1",
    rangeStart: "10.20.0.100",
    rangeEnd: "10.20.0.200",
    leaseTime: "12h",
  };
}

function outsideInterfaceHint(interfaces: HostInterface[], selected: string): string {
  const iface = interfaces.find((item) => item.name === selected);
  if (iface?.default_route) {
    return "This NIC carries the host default route and cannot be used for NAT.";
  }
  if (iface?.physical === false) {
    return "Select a wired physical NIC; virtual and VPP-managed interfaces are not valid outside uplinks.";
  }
  if (iface?.wireless) {
    return "Wireless NICs are not supported as VPP NAT44 outside uplinks; use a wired NIC.";
  }
  return iface
    ? `VPP will take over ${iface.name} and flush its host addresses.`
    : "The selected NIC will be taken over by VPP; its host addresses are flushed.";
}

function typeHint(type: VNFType): string {
  if (type === "nat44") {
    return "IPv4 NAT44 gateway. A dedicated network BVI owns the gateway; the outside NIC is taken over by VPP.";
  }
  if (type === "dhcpv6") {
    return "IPv6 address pool with stateful IA_NA leases on a VPP tap.";
  }
  return "IPv4 address pool served on a VPP tap.";
}

export function VNFDialog({ vnf, networks, vppReady, initialNetworkID, onClose, onSaved }: VNFDialogProps) {
  const [form, setForm] = useState<FormState>(() => defaultForm(vnf, networks, vppReady, initialNetworkID));
  const [hostInterfaces, setHostInterfaces] = useState<HostInterface[]>([]);
  const [hostInterfacesLoading, setHostInterfacesLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [formError, setFormError] = useState("");
  const dialogRef = useRef<HTMLDialogElement | null>(null);
  const nameRef = useRef<HTMLInputElement | null>(null);
  const formRef = useRef<HTMLFormElement | null>(null);

  const editing = vnf !== null;
  const nat44 = form.type === "nat44";

  useEffect(() => {
    dialogRef.current?.showModal();
    nameRef.current?.focus();
  }, []);

  useEffect(() => {
    if (!nat44) {
      return;
    }
    let cancelled = false;
    setHostInterfacesLoading(true);
    api<HostInterface[]>("/api/host/interfaces")
      .then((items) => {
        if (!cancelled) {
          setHostInterfaces(Array.isArray(items) ? items : []);
        }
      })
      .catch(() => {
        if (!cancelled) {
          setHostInterfaces([]);
        }
      })
      .finally(() => {
        if (!cancelled) {
          setHostInterfacesLoading(false);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [nat44]);

  const update = <K extends keyof FormState>(key: K, value: FormState[K]) => {
    setForm((current) => ({ ...current, [key]: value }));
  };

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
    setFormError("");

    if (!form.gateway.trim()) {
      setFormError("Gateway CIDR is required.");
      return;
    }
    if (!nat44 && (!form.rangeStart.trim() || !form.rangeEnd.trim())) {
      setFormError("Range start and end are required.");
      return;
    }
    if (nat44 && !form.outsideInterface) {
      setFormError("Select an outside network interface.");
      return;
    }
    if (
      nat44 &&
      form.enabled &&
      !window.confirm(
        `Take over ${form.outsideInterface} for NAT44?\n\nVPP will own this NIC and its host IPv4/IPv6 addresses will be flushed. The host default-route interface is rejected by the server.`
      )
    ) {
      return;
    }

    const config: VNFConfig = nat44
      ? {
          gateway: form.gateway.trim(),
          outside_interface: form.outsideInterface,
          outside_address: form.outsideAddress.trim(),
          outside_gateway: form.outsideGateway.trim(),
          nat44_pool: form.nat44Pool.trim(),
          dnat_rules: form.dnatRules.map((rule) => ({
            protocol: rule.protocol,
            external_port: Number(rule.external_port),
            local_ip: rule.local_ip.trim(),
            local_port: Number(rule.local_port),
          })),
        }
      : {
          gateway: form.gateway.trim(),
          range_start: form.rangeStart.trim(),
          range_end: form.rangeEnd.trim(),
          dns: form.dns.trim(),
          lease_time: form.leaseTime.trim() || "12h",
        };

    const payload: Record<string, unknown> = {
      name: form.name.trim(),
      enabled: form.enabled,
      config,
    };
    if (!editing) {
      payload.network_id = form.networkID;
      payload.type = form.type;
    }

    setSaving(true);
    try {
      const saved = await api<VNF>(editing ? `/api/vnfs/${vnf?.id}` : "/api/vnfs", {
        method: editing ? "PUT" : "POST",
        body: JSON.stringify(payload),
      });
      onSaved(saved);
    } catch (err) {
      setFormError(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  };

  const addDNATRule = () => {
    setForm((current) => ({
      ...current,
      dnatRules: [
        ...current.dnatRules,
        { protocol: "tcp", external_port: 8080, local_ip: "", local_port: 80 },
      ],
    }));
  };

  const updateDNATRule = <K extends keyof DNATRule>(index: number, key: K, value: DNATRule[K]) => {
    setForm((current) => ({
      ...current,
      dnatRules: current.dnatRules.map((rule, index_) =>
        index_ === index ? { ...rule, [key]: value } : rule
      ),
    }));
  };

  const removeDNATRule = (index: number) => {
    setForm((current) => ({
      ...current,
      dnatRules: current.dnatRules.filter((_, index_) => index_ !== index),
    }));
  };

  const enabledHint = nat44
    ? !vppReady
      ? "VPP is not ready. Save disabled and start the NAT44 VNF later."
      : form.enabled
        ? "Saving takes over the selected outside NIC and flushes its host addresses."
        : "The configuration is saved without taking over the outside NIC."
    : vppReady
      ? "Creates a VPP tap and starts the function immediately."
      : "VPP is not ready. The VNF can be created disabled and started later.";

  return (
    <dialog
      ref={dialogRef}
      id="vnf-dialog"
      class="vnf-dialog"
      aria-labelledby="vnf-dialog-title"
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
            <p class="eyebrow">Network function</p>
            <h2 id="vnf-dialog-title">{editing ? "Edit VNF" : "Add VNF"}</h2>
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
              placeholder="lab-dhcpv4"
              value={form.name}
              onInput={(event) => update("name", event.currentTarget.value)}
            />
            <span class="field-hint">Letters, digits, dots, dashes and underscores.</span>
          </label>

          <label class="field">
            <span class="field-label">Network</span>
            <select
              required
              disabled={editing}
              value={String(form.networkID)}
              onChange={(event) => update("networkID", Number(event.currentTarget.value))}
            >
              {networks.map((network) => (
                <option value={String(network.id)} key={network.id}>
                  {network.name} · bd {network.bridge_domain_id}
                </option>
              ))}
            </select>
          </label>

          <label class="field">
            <span class="field-label">Type</span>
            <select
              required
              disabled={editing}
              value={form.type}
              onChange={(event) => {
                if (!editing) {
                  setForm((current) => applyTypeDefaults(current, event.currentTarget.value as VNFType));
                }
              }}
            >
              <option value="dhcpv4">DHCPv4</option>
              <option value="dhcpv6">DHCPv6</option>
              <option value="nat44">NAT44</option>
            </select>
            <span class="field-hint">{typeHint(form.type)}</span>
          </label>

          <label class="field">
            <span class="field-label">Gateway CIDR</span>
            <input
              type="text"
              required
              placeholder={nat44 ? "10.20.0.1/24 (network BVI gateway)" : form.type === "dhcpv6" ? "fd00:20::1/64" : "10.20.0.1/24"}
              autocomplete="off"
              spellcheck={false}
              value={form.gateway}
              onInput={(event) => update("gateway", event.currentTarget.value)}
            />
          </label>
        </div>

        {!nat44 ? (
          <div class="form-grid">
            <label class="field">
              <span class="field-label">DNS servers <em>optional</em></span>
              <input
                type="text"
                placeholder={form.type === "dhcpv6" ? "2001:4860:4860::8888" : "1.1.1.1,8.8.8.8"}
                autocomplete="off"
                spellcheck={false}
                value={form.dns}
                onInput={(event) => update("dns", event.currentTarget.value)}
              />
            </label>

            <label class="field">
              <span class="field-label">Range start</span>
              <input
                type="text"
                required
                placeholder={form.type === "dhcpv6" ? "fd00:20::100" : "10.20.0.100"}
                autocomplete="off"
                spellcheck={false}
                value={form.rangeStart}
                onInput={(event) => update("rangeStart", event.currentTarget.value)}
              />
            </label>

            <label class="field">
              <span class="field-label">Range end</span>
              <input
                type="text"
                required
                placeholder={form.type === "dhcpv6" ? "fd00:20::200" : "10.20.0.200"}
                autocomplete="off"
                spellcheck={false}
                value={form.rangeEnd}
                onInput={(event) => update("rangeEnd", event.currentTarget.value)}
              />
            </label>

            <label class="field">
              <span class="field-label">Lease time</span>
              <input
                type="text"
                placeholder="12h"
                autocomplete="off"
                spellcheck={false}
                value={form.leaseTime}
                onInput={(event) => update("leaseTime", event.currentTarget.value)}
              />
            </label>
          </div>
        ) : (
          <div class="form-grid">
            <label class="field">
              <span class="field-label">Outside NIC</span>
              <select
                required
                value={form.outsideInterface}
                onChange={(event) => update("outsideInterface", event.currentTarget.value)}
              >
                <option value="" disabled>
                  {hostInterfacesLoading ? "Loading host interfaces…" : "Select a wired physical NIC…"}
                </option>
                {hostInterfaces.map((iface) => {
                  const blocked = iface.default_route || iface.physical === false || iface.wireless;
                  const addresses =
                    Array.isArray(iface.addresses) && iface.addresses.length
                      ? ` — ${iface.addresses.join(", ")}`
                      : "";
                  const reason = iface.default_route
                    ? " (default route — blocked)"
                    : iface.physical === false
                      ? " (virtual — not selectable)"
                      : iface.wireless
                        ? " (wireless — not supported)"
                        : "";
                  return (
                    <option value={iface.name} disabled={blocked} key={iface.name}>
                      {iface.name}
                      {addresses}
                      {reason}
                    </option>
                  );
                })}
                {form.outsideInterface &&
                !hostInterfaces.some((iface) => iface.name === form.outsideInterface) ? (
                  <option value={form.outsideInterface}>
                    {form.outsideInterface} (not currently present)
                  </option>
                ) : null}
              </select>
              <span class="field-hint">{outsideInterfaceHint(hostInterfaces, form.outsideInterface)}</span>
            </label>

            <label class="field">
              <span class="field-label">Outside address <em>optional</em></span>
              <input
                type="text"
                placeholder="empty = VPP DHCP client"
                autocomplete="off"
                spellcheck={false}
                value={form.outsideAddress}
                onInput={(event) => update("outsideAddress", event.currentTarget.value)}
              />
            </label>

            <label class="field">
              <span class="field-label">Outside gateway <em>optional</em></span>
              <input
                type="text"
                placeholder="198.51.100.1"
                autocomplete="off"
                spellcheck={false}
                value={form.outsideGateway}
                onInput={(event) => update("outsideGateway", event.currentTarget.value)}
              />
            </label>

            <label class="field">
              <span class="field-label">NAT44 pool <em>optional</em></span>
              <input
                type="text"
                placeholder="203.0.113.10-203.0.113.20"
                autocomplete="off"
                spellcheck={false}
                value={form.nat44Pool}
                onInput={(event) => update("nat44Pool", event.currentTarget.value)}
              />
            </label>

            <div class="field field-wide">
              <div class="panel-head acl-rules-head">
                <div>
                  <span class="field-label">DNAT / port forwarding <em>optional</em></span>
                  <span class="field-hint">
                    Expose services inside this network on the NAT44 outside address. Saving restarts NAT44.
                  </span>
                </div>
                <button class="button ghost" type="button" onClick={addDNATRule}>
                  Add rule
                </button>
              </div>
              <div class="acl-rule-rows">
                {form.dnatRules.map((rule, index) => (
                  <div class="acl-rule dnat-rule" key={index}>
                    <div class="acl-rule-head">
                      <span class="dnat-rule-title">Rule {index + 1}</span>
                      <button
                        class="row-action danger"
                        type="button"
                        onClick={() => removeDNATRule(index)}
                      >
                        Remove
                      </button>
                    </div>
                    <div class="acl-rule-fields">
                      <label class="field dnat-field-protocol">
                        <span class="field-label">Protocol</span>
                        <select
                          value={rule.protocol}
                          onChange={(event) =>
                            updateDNATRule(index, "protocol", event.currentTarget.value as "tcp" | "udp")
                          }
                        >
                          <option value="tcp">TCP</option>
                          <option value="udp">UDP</option>
                        </select>
                      </label>
                      <label class="field dnat-field-external-port">
                        <span class="field-label">External port</span>
                        <input
                          type="number"
                          min={1}
                          max={65535}
                          step={1}
                          required
                          inputMode="numeric"
                          placeholder="8080"
                          value={rule.external_port || ""}
                          onInput={(event) =>
                            updateDNATRule(index, "external_port", Number(event.currentTarget.value))
                          }
                        />
                      </label>
                      <label class="field dnat-field-local-ip">
                        <span class="field-label">Internal address</span>
                        <input
                          type="text"
                          required
                          autocomplete="off"
                          spellcheck={false}
                          inputMode="decimal"
                          placeholder="10.20.0.100"
                          value={rule.local_ip}
                          onInput={(event) => updateDNATRule(index, "local_ip", event.currentTarget.value)}
                        />
                      </label>
                      <label class="field dnat-field-local-port">
                        <span class="field-label">Internal port</span>
                        <input
                          type="number"
                          min={1}
                          max={65535}
                          step={1}
                          required
                          inputMode="numeric"
                          placeholder="80"
                          value={rule.local_port || ""}
                          onInput={(event) =>
                            updateDNATRule(index, "local_port", Number(event.currentTarget.value))
                          }
                        />
                      </label>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          </div>
        )}

        <label class="field field-wide checkbox-field">
          <span class="checkbox-row">
            <input
              type="checkbox"
              checked={form.enabled}
              disabled={!editing && !vppReady}
              onChange={(event) => update("enabled", event.currentTarget.checked)}
            />
            <span>Enable this VNF</span>
          </span>
          <span class="field-hint">{enabledHint}</span>
        </label>

        {formError ? (
          <p class="form-error" role="alert">
            {formError}
          </p>
        ) : null}

        <footer class="dialog-actions">
          <button class="button ghost" type="button" disabled={saving} onClick={close}>
            Cancel
          </button>
          <button class="button primary" type="submit" disabled={saving}>
            {saving ? "Saving…" : editing ? "Save VNF" : "Add VNF"}
          </button>
        </footer>
      </form>
    </dialog>
  );
}
