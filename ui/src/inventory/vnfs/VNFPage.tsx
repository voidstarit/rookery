import { useEffect, useRef, useState } from "preact/hooks";
import { api } from "../../shared/api";
import type {
  DHCPOption,
  DHCPStaticAssignment,
  DNATRule,
  DNSRecord,
  HostInterface,
  Network,
  VNF,
  VNFConfig,
  VNFType,
} from "../types";

type VNFPageProps = {
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
  staticAssignments: DHCPStaticAssignment[];
  dhcpOptions: DHCPOption[];
  dnsRecords: DNSRecord[];
  dnsForward: boolean;
  dnsForwarders: string;
  transport: "inherit" | "shared" | "dedicated" | "tap" | "memif";
  localAS: string;
  routerID: string;
  peerAS: string;
  peerIP: string;
  localIP: string;
  peerGateway: string;
  nextHop: string;
  multihopTTL: string;
  password: string;
  passive: boolean;
  targetTableID: string;
  advertisePrefixes: string;
};

const NAME_PATTERN = "[A-Za-z0-9][A-Za-z0-9._\\-]*";

function ipv4HostOffset(cidr: string | undefined, offset: number): string {
  if (!cidr) {
    return "";
  }
  const [addr, bits] = cidr.trim().split("/");
  const parts = addr.split(".").map(Number);
  if (parts.length !== 4 || parts.some((part) => !Number.isInteger(part) || part < 0 || part > 255) || !bits) {
    return "";
  }
  const value = ((((parts[0] << 24) >>> 0) + (parts[1] << 16) + (parts[2] << 8) + parts[3] + offset) >>> 0);
  return `${(value >>> 24) & 255}.${(value >>> 16) & 255}.${(value >>> 8) & 255}.${value & 255}`;
}

function firstIPv4Host(cidr: string | undefined): string {
  const host = ipv4HostOffset(cidr, 1);
  if (!host || !cidr) {
    return "";
  }
  return `${host}/${cidr.trim().split("/")[1]}`;
}

function defaultForm(vnf: VNF | null, networks: Network[], vppReady: boolean, initialNetworkID?: number): FormState {
  const type = vnf?.type ?? "dhcpv4";
  const config = vnf?.config ?? {};
  const selectedNetwork = networks.find(
    (network) => network.id === (vnf?.network_id ?? initialNetworkID ?? networks[0]?.id),
  );
  const defaultGateway = firstIPv4Host(selectedNetwork?.ipv4_cidr) || "10.0.0.1/16";
  return {
    name: vnf?.name ?? "",
    networkID: vnf?.network_id ?? initialNetworkID ?? networks[0]?.id ?? 0,
    type,
    enabled: vnf ? vnf.enabled : vppReady,
    gateway: config.gateway ?? (type === "dhcpv6" ? "fd00:20::1/64" : defaultGateway),
    dns: config.dns ?? (type === "dhcpv6" ? "2001:4860:4860::8888" : "1.1.1.1"),
    rangeStart: config.range_start ?? (type === "dhcpv6" ? "fd00:20::100" : "10.20.0.100"),
    rangeEnd: config.range_end ?? (type === "dhcpv6" ? "fd00:20::200" : "10.20.0.200"),
    leaseTime: config.lease_time ?? "12h",
    outsideInterface: config.outside_interface ?? "",
    outsideAddress: config.outside_address ?? "",
    outsideGateway: config.outside_gateway ?? "",
    nat44Pool: config.nat44_pool ?? "",
    dnatRules: Array.isArray(config.dnat_rules) ? config.dnat_rules.map((rule) => ({ ...rule })) : [],
    staticAssignments: Array.isArray(config.static_assignments)
      ? config.static_assignments.map((assignment) => ({ ...assignment }))
      : [],
    dhcpOptions: Array.isArray(config.dhcp_options)
      ? config.dhcp_options.map((option) => ({ ...option }))
      : [],
    dnsRecords: Array.isArray(config.dns_records)
      ? config.dns_records.map((record) => ({ ...record }))
      : [],
    dnsForward: config.dns_forward ?? false,
    dnsForwarders: Array.isArray(config.dns_forwarders)
      ? config.dns_forwarders.join(", ")
      : vnf?.type === "dns"
        ? ""
        : "1.1.1.1, 8.8.8.8",
    transport:
      type === "bgp"
        ? config.transport === "tap"
          ? "tap"
          : "memif"
        : config.transport === "shared" || config.transport === "dedicated" || config.transport === "tap"
          ? config.transport
          : "inherit",
    localAS: String(config.local_as ?? 64512),
    routerID: config.router_id ?? "10.20.0.1",
    peerAS: String(config.peer_as ?? 64513),
    peerIP: config.peer_ip ?? "169.254.0.2",
    localIP: config.local_ip ?? "169.254.0.1/30",
    peerGateway: config.peer_gateway ?? "",
    nextHop: config.next_hop ?? "",
    multihopTTL: String(config.multihop_ttl ?? 1),
    password: config.password ?? "",
    passive: config.passive ?? false,
    targetTableID: String(config.target_table_id ?? 0),
    advertisePrefixes: Array.isArray(config.advertise_prefixes) ? config.advertise_prefixes.join("\n") : "",
  };
}

function applyTypeDefaults(state: FormState, type: VNFType): FormState {
  if (type === "dns") {
    return {
      ...state,
      type,
      dnsRecords: [],
      dnsForward: true,
      dnsForwarders: "1.1.1.1, 8.8.8.8",
    };
  }
  if (type === "bgp") {
    return {
      ...state,
      type,
      transport: "memif",
      localAS: "64512",
      routerID: "10.20.0.1",
      peerAS: "64513",
      peerIP: "169.254.0.2",
      localIP: "169.254.0.1/30",
      peerGateway: "",
      nextHop: "",
      multihopTTL: "1",
      password: "",
      passive: false,
      targetTableID: "0",
      advertisePrefixes: "",
    };
  }
  if (type === "dhcpv6") {
    return {
      ...state,
      type,
      transport: "tap",
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
    transport: "inherit",
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
  if (type === "dns") {
    return "Local DNS server on the network service address (.2). Serve your own A/AAAA/CNAME/TXT records and optionally forward other names upstream.";
  }
  if (type === "bgp") {
    return "Embedded IPv4/IPv6 BGP speaker. Select memif for userspace/gVisor or tap for kernel TCP with TCP MD5 support.";
  }
  if (type === "nat44") {
    return "IPv4 NAT44 gateway. A dedicated network BVI owns the gateway; the outside NIC is taken over by VPP.";
  }
  if (type === "dhcpv6") {
    return "IPv6 address pool with stateful IA_NA leases on a VPP tap (shared memif is DHCPv4-only for now).";
  }
  return "IPv4 address pool served on the network-shared memif by default (VNF address .2).";
}

type DHCPOptionPreset = {
  code: number;
  label: string;
  format: string;
  placeholder: string;
  hint?: string;
};

const DHCPV4_OPTION_PRESETS: DHCPOptionPreset[] = [
  { code: 3, label: "Router", format: "ipv4", placeholder: "10.20.0.1", hint: "Overrides the gateway router option." },
  { code: 6, label: "DNS servers", format: "ipv4", placeholder: "10.20.0.2, 1.1.1.1", hint: "Overrides the DNS field." },
  { code: 15, label: "Domain name", format: "string", placeholder: "lab.test" },
  { code: 42, label: "NTP servers", format: "ipv4", placeholder: "10.20.0.1, 10.20.0.2" },
  { code: 44, label: "WINS servers", format: "ipv4", placeholder: "10.20.0.10" },
  { code: 66, label: "TFTP server name", format: "string", placeholder: "tftp.lab.test" },
  { code: 67, label: "Bootfile name", format: "string", placeholder: "pxelinux.0" },
  { code: 119, label: "Domain search list", format: "domain-list", placeholder: "lab.test, apps.lab.test" },
  { code: 252, label: "WPAD URL", format: "string", placeholder: "http://wpad.lab.test/wpad.dat" },
];

const DHCPV6_OPTION_PRESETS: DHCPOptionPreset[] = [
  { code: 21, label: "SIP domain names", format: "domain-list", placeholder: "sip.lab.test" },
  { code: 22, label: "SIP servers", format: "ipv6", placeholder: "fd00:20::10" },
  { code: 23, label: "DNS recursive name servers", format: "ipv6", placeholder: "fd00:20::2", hint: "Overrides the DNS field." },
  { code: 24, label: "Domain search list", format: "domain-list", placeholder: "lab.test, apps.lab.test" },
  { code: 31, label: "SNTP servers", format: "ipv6", placeholder: "fd00:20::10" },
  { code: 59, label: "Bootfile URL", format: "string", placeholder: "http://[fd00:20::1]/boot" },
  { code: 60, label: "Bootfile parameters", format: "string", placeholder: "console=ttyS0" },
];

function dhcpOptionPresets(type: VNFType): DHCPOptionPreset[] {
  return type === "dhcpv6" ? DHCPV6_OPTION_PRESETS : DHCPV4_OPTION_PRESETS;
}

function dhcpOptionPreset(type: VNFType, code: number): DHCPOptionPreset | undefined {
  return dhcpOptionPresets(type).find((preset) => preset.code === code);
}

export function VNFPage({ vnf, networks, vppReady, initialNetworkID, onClose, onSaved }: VNFPageProps) {
  const [form, setForm] = useState<FormState>(() => defaultForm(vnf, networks, vppReady, initialNetworkID));
  const [hostInterfaces, setHostInterfaces] = useState<HostInterface[]>([]);
  const [hostInterfacesLoading, setHostInterfacesLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [formError, setFormError] = useState("");
  const nameRef = useRef<HTMLInputElement | null>(null);
  const formRef = useRef<HTMLFormElement | null>(null);

  const editing = vnf !== null;
  const nat44 = form.type === "nat44";
  const bgp = form.type === "bgp";
  const dns = form.type === "dns";
  const dnsServiceIP = ipv4HostOffset(
    networks.find((item) => item.id === form.networkID)?.ipv4_cidr,
    2,
  );

  useEffect(() => {
    nameRef.current?.focus();
    document.title = `${vnf ? "Edit" : "Add"} VNF · rookery`;
  }, [vnf]);

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
      onClose();
    }
  };

  const submit = async (event: Event) => {
    event.preventDefault();
    if (saving || !formRef.current?.reportValidity()) {
      return;
    }
    setFormError("");

    if (!bgp && !dns && !form.gateway.trim()) {
      setFormError("Gateway CIDR is required.");
      return;
    }
    if (!bgp && !nat44 && !dns && (!form.rangeStart.trim() || !form.rangeEnd.trim())) {
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

    const advertised = form.advertisePrefixes
      .split(/[\n,]+/)
      .map((value) => value.trim())
      .filter(Boolean);
    const config: VNFConfig = bgp
      ? {
          transport: form.transport === "tap" ? "tap" : "memif",
          local_as: Number(form.localAS),
          router_id: form.routerID.trim(),
          peer_as: Number(form.peerAS),
          peer_ip: form.peerIP.trim(),
          local_ip: form.localIP.trim(),
          peer_gateway: form.peerGateway.trim(),
          next_hop: form.nextHop.trim(),
          multihop_ttl: Number(form.multihopTTL),
          password: form.transport === "tap" ? form.password : "",
          passive: form.passive,
          target_table_id: Number(form.targetTableID),
          advertise_prefixes: advertised,
        }
      : dns
      ? {
          dns_records: form.dnsRecords.map((record) => ({
            name: record.name.trim(),
            type: record.type,
            value: record.value.trim(),
            ttl: Number(record.ttl) || 0,
          })),
          dns_forward: form.dnsForward,
          dns_forwarders: form.dnsForwarders
            .split(/[\n,]+/)
            .map((value) => value.trim())
            .filter(Boolean),
        }
      : nat44
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
          dhcp_options: form.dhcpOptions.map((option) => ({
            code: Number(option.code),
            format: option.format,
            value: option.value.trim(),
          })),
          ...(form.transport === "shared" || form.transport === "dedicated" || form.transport === "tap"
            ? { transport: form.transport }
            : {}),
          ...(form.type === "dhcpv4"
            ? {
                static_assignments: form.staticAssignments.map((assignment) => ({
                  mac: assignment.mac.trim(),
                  ip: assignment.ip.trim(),
                })),
              }
            : {}),
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

  const addStaticAssignment = () => {
    setForm((current) => ({
      ...current,
      staticAssignments: [...current.staticAssignments, { mac: "", ip: "" }],
    }));
  };

  const updateStaticAssignment = <K extends keyof DHCPStaticAssignment>(
    index: number,
    key: K,
    value: DHCPStaticAssignment[K]
  ) => {
    setForm((current) => ({
      ...current,
      staticAssignments: current.staticAssignments.map((assignment, index_) =>
        index_ === index ? { ...assignment, [key]: value } : assignment
      ),
    }));
  };

  const removeStaticAssignment = (index: number) => {
    setForm((current) => ({
      ...current,
      staticAssignments: current.staticAssignments.filter((_, index_) => index_ !== index),
    }));
  };

  const addDHCPOption = () => {
    setForm((current) => ({
      ...current,
      dhcpOptions: [...current.dhcpOptions, { code: 15, format: "string", value: "" }],
    }));
  };

  const updateDHCPOption = <K extends keyof DHCPOption>(index: number, key: K, value: DHCPOption[K]) => {
    setForm((current) => ({
      ...current,
      dhcpOptions: current.dhcpOptions.map((option, index_) =>
        index_ === index ? { ...option, [key]: value } : option
      ),
    }));
  };

  const updateDHCPOptionCode = (index: number, code: number) => {
    const preset = dhcpOptionPreset(form.type, code);
    setForm((current) => ({
      ...current,
      dhcpOptions: current.dhcpOptions.map((option, index_) =>
        index_ === index ? { ...option, code, ...(preset ? { format: preset.format } : {}) } : option
      ),
    }));
  };

  const removeDHCPOption = (index: number) => {
    setForm((current) => ({
      ...current,
      dhcpOptions: current.dhcpOptions.filter((_, index_) => index_ !== index),
    }));
  };

  const addDNSRecord = () => {
    setForm((current) => ({
      ...current,
      dnsRecords: [...current.dnsRecords, { name: "", type: "A", value: "", ttl: 300 }],
    }));
  };

  const updateDNSRecord = <K extends keyof DNSRecord>(index: number, key: K, value: DNSRecord[K]) => {
    setForm((current) => ({
      ...current,
      dnsRecords: current.dnsRecords.map((record, index_) =>
        index_ === index ? { ...record, [key]: value } : record
      ),
    }));
  };

  const removeDNSRecord = (index: number) => {
    setForm((current) => ({
      ...current,
      dnsRecords: current.dnsRecords.filter((_, index_) => index_ !== index),
    }));
  };

  const enabledHint = bgp
    ? vppReady
      ? form.transport === "memif"
        ? "Creates a memif and starts the embedded BGP speaker on gVisor. TCP MD5 is unavailable on this transport."
        : "Creates a VPP tap and starts the embedded BGP speaker on kernel TCP; TCP MD5 is supported."
      : "VPP is not ready. The BGP VNF can be created disabled and started later."
    : dns
    ? vppReady
      ? "Starts a DNS server on the network service address (.2, UDP/TCP 53) using the shared VNF memif."
      : "VPP is not ready. The DNS VNF can be created disabled and started later."
    : nat44
    ? !vppReady
      ? "VPP is not ready. Save disabled and start the NAT44 VNF later."
      : form.enabled
        ? "Saving takes over the selected outside NIC and flushes its host addresses."
        : "The configuration is saved without taking over the outside NIC."
    : vppReady
      ? "Creates a VPP tap and starts the function immediately."
      : "VPP is not ready. The VNF can be created disabled and started later.";

  return (
    <div class="vnf-page">
      <form ref={formRef} class="vnf-page-form" novalidate onSubmit={(event) => void submit(event)}>
        <header class="route-page-head">
          <div>
            <button class="back-link" type="button" disabled={saving} onClick={close}>
              ← VNFs
            </button>
            <p class="eyebrow">Network function</p>
            <h2 id="vnf-dialog-title">{editing ? `Edit ${vnf?.name ?? "VNF"}` : "Add VNF"}</h2>
          </div>
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
              onChange={(event) => {
                const networkID = Number(event.currentTarget.value);
                const network = networks.find((item) => item.id === networkID);
                setForm((current) => ({
                  ...current,
                  networkID,
                  gateway:
                    current.type === "dhcpv6"
                      ? current.gateway
                      : firstIPv4Host(network?.ipv4_cidr) || current.gateway,
                }));
              }}
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
                  const type = event.currentTarget.value as VNFType;
                  setForm((current) => {
                    const next = applyTypeDefaults(current, type);
                    const network = networks.find((item) => item.id === next.networkID);
                    if ((type === "dhcpv4" || type === "nat44") && network?.ipv4_cidr) {
                      next.gateway = firstIPv4Host(network.ipv4_cidr) || next.gateway;
                    }
                    return next;
                  });
                }
              }}
            >
              <option value="dhcpv4">DHCPv4</option>
              <option value="dhcpv6">DHCPv6</option>
              <option value="nat44">NAT44</option>
              <option value="bgp">BGP</option>
              <option value="dns">DNS</option>
            </select>
            <span class="field-hint">{typeHint(form.type)}</span>
          </label>

          {!bgp && !dns ? (
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
              <span class="field-hint">
                Network range: {networks.find((item) => item.id === form.networkID)?.ipv4_cidr || "not set"}
              </span>
            </label>
          ) : null}
        </div>

        {!nat44 && !bgp && !dns ? (
          <>
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

            {form.type === "dhcpv4" ? (
              <label class="field">
                <span class="field-label">VNF networking</span>
                <select
                  value={form.transport}
                  onChange={(event) => update("transport", event.currentTarget.value as FormState["transport"])}
                >
                  <option value="inherit">Inherit network default (shared)</option>
                  <option value="shared">Shared network memif</option>
                  <option value="dedicated">Dedicated memif</option>
                  <option value="tap">Tap</option>
                </select>
                <span class="field-hint">
                  Shared uses one memif per network; the stack owns .2 and the BVI owns .1.
                </span>
              </label>
            ) : null}

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
          {form.type === "dhcpv4" ? (
            <div class="form-grid">
              <div class="field field-wide">
                <div class="panel-head acl-rules-head">
                  <div>
                    <span class="field-label">Static assignments <em>optional</em></span>
                    <span class="field-hint">
                      Reserve an IPv4 address for a MAC address. Reserved addresses are excluded
                      from the dynamic pool and can be inside or outside the DHCP range.
                    </span>
                  </div>
                  <button class="button ghost" type="button" onClick={addStaticAssignment}>
                    Add assignment
                  </button>
                </div>
                <div class="acl-rule-rows">
                  {form.staticAssignments.map((assignment, index) => (
                    <div class="acl-rule" key={index}>
                      <div class="acl-rule-head">
                        <span class="dnat-rule-title">Assignment {index + 1}</span>
                        <button
                          class="row-action danger"
                          type="button"
                          onClick={() => removeStaticAssignment(index)}
                        >
                          Remove
                        </button>
                      </div>
                      <div class="acl-rule-fields">
                        <label class="field dhcp-static-field">
                          <span class="field-label">MAC address</span>
                          <input
                            type="text"
                            required
                            autocomplete="off"
                            spellcheck={false}
                            placeholder="02:00:00:00:00:01"
                            value={assignment.mac}
                            onInput={(event) =>
                              updateStaticAssignment(index, "mac", event.currentTarget.value)
                            }
                          />
                        </label>
                        <label class="field dhcp-static-field">
                          <span class="field-label">IP address</span>
                          <input
                            type="text"
                            required
                            autocomplete="off"
                            spellcheck={false}
                            inputMode="decimal"
                            placeholder="10.20.0.50"
                            value={assignment.ip}
                            onInput={(event) =>
                              updateStaticAssignment(index, "ip", event.currentTarget.value)
                            }
                          />
                        </label>
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            </div>
          ) : null}
          <div class="form-grid">
            <div class="field field-wide">
              <div class="panel-head acl-rules-head">
                <div>
                  <span class="field-label">Extra DHCP options <em>optional</em></span>
                  <span class="field-hint">
                    Advertise additional options such as option 15 (domain name), 42 (NTP),
                    119 (domain search), or DHCPv6 option 24 (domain search). Use
                    <code>string</code>, <code>ipv4</code>, <code>ipv6</code>, <code>hex</code>,
                    <code>uint8</code>, <code>uint16</code>, <code>uint32</code>, or
                    <code>domain-list</code>. DNS server options are set through the DNS field.
                  </span>
                </div>
                <button class="button ghost" type="button" onClick={addDHCPOption}>
                  Add option
                </button>
              </div>
              <div class="acl-rule-rows">
                {form.dhcpOptions.map((option, index) => (
                  <div class="acl-rule" key={index}>
                    <div class="acl-rule-head">
                      <span class="dnat-rule-title">Option {index + 1}</span>
                      <button
                        class="row-action danger"
                        type="button"
                        onClick={() => removeDHCPOption(index)}
                      >
                        Remove
                      </button>
                    </div>
                    <div class="acl-rule-fields">
                      <label class="field">
                        <span class="field-label">Code</span>
                        <input
                          type="number"
                          required
                          min={1}
                          max={form.type === "dhcpv6" ? 65535 : 254}
                          list={`dhcp-option-codes-${form.type}`}
                          value={option.code || ""}
                          onInput={(event) =>
                            updateDHCPOptionCode(index, Number(event.currentTarget.value))
                          }
                        />
                        {(() => {
                          const preset = dhcpOptionPreset(form.type, option.code);
                          return (
                            <span class="field-hint">
                              {preset ? `${preset.label}${preset.hint ? ` — ${preset.hint}` : ""}` : "Custom option code"}
                            </span>
                          );
                        })()}
                      </label>
                      <label class="field">
                        <span class="field-label">Format</span>
                        <select
                          value={option.format}
                          onChange={(event) => updateDHCPOption(index, "format", event.currentTarget.value)}
                        >
                          <option value="string">string</option>
                          <option value="ipv4">ipv4</option>
                          <option value="ipv6">ipv6</option>
                          <option value="hex">hex</option>
                          <option value="uint8">uint8</option>
                          <option value="uint16">uint16</option>
                          <option value="uint32">uint32</option>
                          <option value="domain-list">domain-list</option>
                        </select>
                      </label>
                      <label class="field">
                        <span class="field-label">Value</span>
                        <input
                          type="text"
                          required
                          autocomplete="off"
                          spellcheck={false}
                          placeholder={
                            dhcpOptionPreset(form.type, option.code)?.placeholder ??
                            (option.format === "ipv4"
                              ? "10.20.0.1, 10.20.0.2"
                              : option.format === "ipv6"
                                ? "fd00:20::1"
                                : option.format === "domain-list"
                                  ? "lab.test, apps.lab.test"
                                  : option.format === "hex"
                                    ? "01:02:ff"
                                    : "lab.test")
                          }
                          value={option.value}
                          onInput={(event) => updateDHCPOption(index, "value", event.currentTarget.value)}
                        />
                      </label>
                    </div>
                  </div>
                ))}
                {form.dhcpOptions.length === 0 ? (
                  <p class="field-hint">
                    No extra options. Example: code 15, format string, value lab.test advertises
                    the domain name. DNS and router options override the DNS field and gateway.
                  </p>
                ) : null}
              </div>
              <datalist id={`dhcp-option-codes-${form.type}`}>
                {dhcpOptionPresets(form.type).map((preset) => (
                  <option value={preset.code} label={`${preset.label} (${preset.format})`} />
                ))}
              </datalist>
            </div>
          </div>
          </>
        ) : dns ? (
          <>
            <p class="field-hint">
              Clients on this network should use <code>{dnsServiceIP || "the .2 service address"}</code> as
              their DNS server. Set that address in the DHCP VNF's DNS field (or configure it directly on
              clients).
            </p>
            <div class="form-grid">
              <div class="field field-wide">
                <div class="panel-head acl-rules-head">
                  <div>
                    <span class="field-label">DNS records</span>
                    <span class="field-hint">
                      Names are served authoritatively. Use a fully qualified name such as
                      vm1.lab1.test; a leading *. wildcard is supported. Types: A, AAAA, CNAME, TXT.
                    </span>
                  </div>
                  <button class="button ghost" type="button" onClick={addDNSRecord}>
                    Add record
                  </button>
                </div>
                <div class="acl-rule-rows">
                  {form.dnsRecords.map((record, index) => (
                    <div class="acl-rule" key={index}>
                      <div class="acl-rule-head">
                        <span class="dnat-rule-title">Record {index + 1}</span>
                        <button
                          class="row-action danger"
                          type="button"
                          onClick={() => removeDNSRecord(index)}
                        >
                          Remove
                        </button>
                      </div>
                      <div class="acl-rule-fields">
                        <label class="field">
                          <span class="field-label">Name</span>
                          <input
                            type="text"
                            required
                            autocomplete="off"
                            spellcheck={false}
                            placeholder="vm1.lab1.test"
                            value={record.name}
                            onInput={(event) => updateDNSRecord(index, "name", event.currentTarget.value)}
                          />
                        </label>
                        <label class="field">
                          <span class="field-label">Type</span>
                          <select
                            value={record.type}
                            onChange={(event) =>
                              updateDNSRecord(index, "type", event.currentTarget.value as DNSRecord["type"])
                            }
                          >
                            <option value="A">A</option>
                            <option value="AAAA">AAAA</option>
                            <option value="CNAME">CNAME</option>
                            <option value="TXT">TXT</option>
                          </select>
                        </label>
                        <label class="field">
                          <span class="field-label">Value</span>
                          <input
                            type="text"
                            required
                            autocomplete="off"
                            spellcheck={false}
                            placeholder={record.type === "A" ? "10.20.0.50" : record.type === "AAAA" ? "fd00:20::50" : record.type === "CNAME" ? "vm1.lab1.test" : "text"}
                            value={record.value}
                            onInput={(event) => updateDNSRecord(index, "value", event.currentTarget.value)}
                          />
                        </label>
                        <label class="field">
                          <span class="field-label">TTL</span>
                          <input
                            type="number"
                            min={0}
                            max={86400}
                            placeholder="300"
                            value={record.ttl ?? 300}
                            onInput={(event) =>
                              updateDNSRecord(index, "ttl", Number(event.currentTarget.value) || 0)
                            }
                          />
                        </label>
                      </div>
                    </div>
                  ))}
                  {form.dnsRecords.length === 0 ? (
                    <p class="field-hint">No local records yet. Add one, or use this VNF only as a forwarder.</p>
                  ) : null}
                </div>
              </div>
            </div>
            <div class="form-grid">
              <label class="field field-wide checkbox-field">
                <span class="checkbox-row">
                  <input
                    type="checkbox"
                    checked={form.dnsForward}
                    onChange={(event) => update("dnsForward", event.currentTarget.checked)}
                  />
                  <span>Forward names without a local record to upstream DNS servers</span>
                </span>
                <span class="field-hint">
                  When disabled, unknown names receive NXDOMAIN. Local records always take precedence.
                </span>
              </label>
              <label class="field field-wide">
                <span class="field-label">Upstream DNS servers</span>
                <input
                  type="text"
                  disabled={!form.dnsForward}
                  autocomplete="off"
                  spellcheck={false}
                  placeholder="1.1.1.1, 8.8.8.8:53"
                  value={form.dnsForwarders}
                  onInput={(event) => update("dnsForwarders", event.currentTarget.value)}
                />
                <span class="field-hint">
                  Comma-separated IPv4/IPv6 addresses or IP:port. Leave empty to use
                  /etc/resolv.conf on the server.
                </span>
              </label>
            </div>
          </>
        ) : bgp ? (
          <>
            <div class="form-grid">
              <label class="field">
                <span class="field-label">Transport</span>
                <select
                  value={form.transport}
                  onChange={(event) => update("transport", event.currentTarget.value as "memif" | "tap")}
                >
                  <option value="memif">memif + gVisor</option>
                  <option value="tap">VPP tap + kernel TCP</option>
                </select>
                <span class="field-hint">
                  TCP MD5 passwords require the tap transport; memif/gVisor does not support TCP MD5.
                </span>
              </label>
              <label class="field checkbox-field">
                <span class="checkbox-row">
                  <input
                    type="checkbox"
                    checked={form.passive}
                    onChange={(event) => update("passive", event.currentTarget.checked)}
                  />
                  <span>Passive mode — wait for the peer to connect</span>
                </span>
              </label>
              <label class="field">
                <span class="field-label">Local AS</span>
                <input
                  type="number"
                  required
                  min={1}
                  value={form.localAS}
                  onInput={(event) => update("localAS", event.currentTarget.value)}
                />
              </label>
              <label class="field">
                <span class="field-label">Router ID</span>
                <input
                  type="text"
                  required
                  placeholder="10.20.0.1"
                  autocomplete="off"
                  value={form.routerID}
                  onInput={(event) => update("routerID", event.currentTarget.value)}
                />
              </label>
              <label class="field">
                <span class="field-label">Peer AS</span>
                <input
                  type="number"
                  required
                  min={1}
                  value={form.peerAS}
                  onInput={(event) => update("peerAS", event.currentTarget.value)}
                />
              </label>
              <label class="field">
                <span class="field-label">Peer IPv4/IPv6 address</span>
                <input
                  type="text"
                  required
                  placeholder="169.254.0.2"
                  autocomplete="off"
                  value={form.peerIP}
                  onInput={(event) => update("peerIP", event.currentTarget.value)}
                />
              </label>
              <label class="field">
                <span class="field-label">Local IPv4/IPv6 CIDR</span>
                <input
                  type="text"
                  required
                  placeholder="169.254.0.1/30"
                  autocomplete="off"
                  value={form.localIP}
                  onInput={(event) => update("localIP", event.currentTarget.value)}
                />
              </label>
              <label class="field">
                <span class="field-label">Peer gateway <em>optional</em></span>
                <input
                  type="text"
                  placeholder="169.254.0.2 (for routed/multihop peers)"
                  autocomplete="off"
                  value={form.peerGateway}
                  onInput={(event) => update("peerGateway", event.currentTarget.value)}
                />
              </label>
              <label class="field">
                <span class="field-label">Advertised next hop <em>optional</em></span>
                <input
                  type="text"
                  placeholder="defaults to local_ip"
                  autocomplete="off"
                  value={form.nextHop}
                  onInput={(event) => update("nextHop", event.currentTarget.value)}
                />
              </label>
              <label class="field">
                <span class="field-label">Multihop TTL</span>
                <input
                  type="number"
                  required
                  min={1}
                  max={255}
                  value={form.multihopTTL}
                  onInput={(event) => update("multihopTTL", event.currentTarget.value)}
                />
              </label>
              <label class="field">
                <span class="field-label">Target VPP table ID</span>
                <input
                  type="number"
                  required
                  min={0}
                  value={form.targetTableID}
                  onInput={(event) => update("targetTableID", event.currentTarget.value)}
                />
                <span class="field-hint">0 is the default VRF; received IPv4 routes are installed here.</span>
              </label>
              <label class="field">
                <span class="field-label">TCP MD5 password <em>tap only</em></span>
                <input
                  type="password"
                  disabled={form.transport === "memif"}
                  placeholder={form.transport === "memif" ? "Unavailable on memif" : "optional"}
                  autocomplete="new-password"
                  value={form.password}
                  onInput={(event) => update("password", event.currentTarget.value)}
                />
              </label>
              <label class="field field-wide">
                <span class="field-label">Advertised IPv4 prefixes <em>optional</em></span>
                <textarea
                  rows={4}
                  placeholder={"10.10.0.0/16\n10.20.0.0/24"}
                  spellcheck={false}
                  value={form.advertisePrefixes}
                  onInput={(event) => update("advertisePrefixes", event.currentTarget.value)}
                />
                <span class="field-hint">One IPv4 CIDR per line. These prefixes are advertised to the peer.</span>
              </label>
            </div>
          </>
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
    </div>
  );
}
