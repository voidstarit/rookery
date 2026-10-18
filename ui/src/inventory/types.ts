export type Network = {
  id: number;
  name: string;
  bridge_domain_id: number;
};

export type Media = {
  id: number;
  name: string;
  path: string;
  size_bytes: number;
  created_at: string;
  updated_at: string;
};

export type VM = {
  id: string;
  name: string;
  status: string;
  boot_media_id?: number;
};

export type VNFType = "dhcpv4" | "dhcpv6" | "nat44";

export type DNATRule = {
  protocol: "tcp" | "udp";
  external_port: number;
  local_ip: string;
  local_port: number;
};

export type VNFConfig = {
  gateway?: string;
  range_start?: string;
  range_end?: string;
  dns?: string;
  lease_time?: string;
  bind_ip?: string;
  outside_interface?: string;
  outside_address?: string;
  outside_gateway?: string;
  nat44_pool?: string;
  dnat_rules?: DNATRule[];
};

export type VNF = {
  id: number;
  name: string;
  network_id: number;
  network_name?: string;
  type: VNFType;
  enabled: boolean;
  config: VNFConfig;
  tap_sw_if_index?: number;
  tap_host_name?: string;
  status?: "running" | "disabled" | "degraded";
  status_detail?: string;
  lease_count?: number;
  created_at: string;
  updated_at: string;
};

export type HostInterface = {
  name: string;
  up: boolean;
  addresses?: string[];
  default_route: boolean;
  physical: boolean;
  wireless: boolean;
};

export type VNFLease = {
  client_id: string;
  address: string;
  hostname?: string;
  expires_at: string;
  active: boolean;
};

export type StatusResponse = {
  vpp?: {
    ready: boolean;
  };
};
