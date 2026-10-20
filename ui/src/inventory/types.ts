export type Network = {
  id: number;
  name: string;
  bridge_domain_id: number;
  bvi_host_name?: string;
  ipv4_gateway?: string;
  created_at?: string;
  updated_at?: string;
};

export type Media = {
  id: number;
  name: string;
  path: string;
  size_bytes: number;
  created_at: string;
  updated_at: string;
};

export type VMDisk = {
  id: number;
  name: string;
  size_gb: number;
  storage_backend_id?: number;
  storage_backend_name?: string;
  vg: string;
  thin_pool: string;
  device: string;
};

export type VMMedia = {
  id: number;
  name: string;
  path: string;
  size_bytes: number;
};

export type VMInterface = {
  id: number;
  name: string;
  network_id: number;
  network_name: string;
  socket_path: string;
  mac: string;
  sw_if_index: number;
  ingress_acl_name?: string;
  egress_acl_name?: string;
};

export type VM = {
  id: string;
  name: string;
  description: string;
  status: string;
  vcpus: number;
  memory_mb: number;
  disk_id?: number;
  disk?: VMDisk;
  disk_ids?: number[];
  disks?: VMDisk[];
  boot_media_id?: number;
  boot_media?: VMMedia;
  interfaces?: VMInterface[];
  console_enabled: boolean;
  created_at?: string;
  updated_at?: string;
};

export type VMMetrics = {
  running: boolean;
  pid?: number;
  uptime_seconds: number;
  cpu_seconds_total: number;
  memory_rss_bytes: number;
  memory_virtual_bytes: number;
  disk_read_bytes_total: number;
  disk_write_bytes_total: number;
  vm_id: string;
  name: string;
  vcpus: number;
  memory_mb: number;
  network_rx_bytes_total: number;
  network_tx_bytes_total: number;
  network_rx_packets_total: number;
  network_tx_packets_total: number;
  sampled_at: string;
};

export type StorageBackendConfig = {
  vg: string;
  thin_pool: string;
  supports_templates?: boolean;
  preparation_script?: string;
  preparation_timeout?: string;
  allow_template_delete_with_clones?: boolean;
};

export type StorageBackend = {
  id: number;
  name: string;
  title: string;
  description: string;
  driver: string;
  config: StorageBackendConfig;
  disk_count: number;
  created_at: string;
  updated_at: string;
};

export type Disk = {
  id: number;
  name: string;
  size_gb: number;
  storage_backend_id?: number;
  storage_backend_name?: string;
  vg: string;
  thin_pool: string;
  device: string;
  attached_vm_id?: string;
  attached_vm_name?: string;
  is_template?: boolean;
  template_disk_id?: number;
  template_disk_name?: string;
  created_at: string;
  updated_at: string;
};

export type ImportableDisk = {
  name: string;
  size_gb: number;
  registered: boolean;
  disk_id?: number;
  attached_vm_id?: string;
  attached_vm_name?: string;
  storage_backend_name?: string;
};

export type VirtualInterface = {
  id: number;
  name: string;
  network_id: number;
  network_name: string;
  socket_path: string;
  mac: string;
  sw_if_index: number;
  attached_vm_id?: string;
  attached_vm_name?: string;
  ingress_acl_id?: number;
  ingress_acl_name?: string;
  egress_acl_id?: number;
  egress_acl_name?: string;
  ingress_acls?: string[];
  egress_acls?: string[];
  ipv4_table_id?: number;
  ipv6_table_id?: number;
  ipv4_table_name?: string;
  ipv6_table_name?: string;
  created_at: string;
  updated_at: string;
};

export type RouteTable = {
  id: number;
  name: string;
  table_id: number;
  ipv6: boolean;
  is_default?: boolean;
  interface_count?: number;
  route_count?: number;
  created_at?: string;
  updated_at?: string;
};

export type Route = {
  id: number;
  route_table_id: number;
  table_id: number;
  ipv6: boolean;
  prefix: string;
  next_hop: string;
  interface_id?: number;
  interface_name?: string;
  created_at: string;
  updated_at: string;
};

export type FIBPath = {
  sw_if_index: number;
  interface_name?: string;
  next_hop?: string;
  type: string;
  weight?: number;
  preference?: number;
  proto?: string;
};

export type FIBRoute = {
  prefix: string;
  table_id: number;
  ipv6: boolean;
  source: number;
  source_name: string;
  dynamic: boolean;
  stats_index?: number;
  paths?: FIBPath[];
};

export type VPPInterface = {
  name: string;
  sw_if_index: number;
  type?: string;
  mac?: string;
  mtu?: number;
  up: boolean;
  tag?: string;
};

export type PacketCaptureStatus = {
  active: boolean;
  interface?: string;
  direction?: string;
  file?: string;
  size?: number;
  max_packets?: number;
  max_bytes?: number;
  snap_length?: number;
  packets?: number;
  buffered?: number;
  started_at?: string;
  stopped_at?: string;
  raw?: string;
};

export type PacketCaptureFile = {
  name: string;
  size: number;
  modified: string;
};

export type PacketCaptureOverview = {
  status: PacketCaptureStatus;
  files: PacketCaptureFile[];
};

export type ACLRuleCounter = {
  // Proto3 JSON omits zero values; a missing index is index 0.
  acl_index?: number;
  rule_index?: number;
  action?: string;
  matched?: number;
};

export type ACLDirectionCounters = {
  matched?: number;
  denied?: number;
  permitted?: number;
  acl_indexes?: number[];
  rules?: ACLRuleCounter[];
};

export type ACLInterfaceCounters = {
  interface: string;
  sw_if_index?: number;
  ingress?: ACLDirectionCounters;
  egress?: ACLDirectionCounters;
};

export type VPPGraphNode = {
  id: string;
  label: string;
  width?: number;
  font_size?: number;
  fill?: number;
};

export type VPPGraphEdge = {
  source: string;
  target: string;
};

export type VPPGraph = {
  nodes: VPPGraphNode[];
  edges: VPPGraphEdge[];
  generated_at: string;
};

export type ACLRule = {
  action: string;
  description?: string;
  protocol: string;
  source: string;
  destination: string;
  source_port_start?: number;
  source_port_end?: number;
  destination_port_start?: number;
  destination_port_end?: number;
};

export type ACL = {
  id: number;
  name: string;
  title: string;
  description: string;
  rules: ACLRule[];
  vpp_index?: number;
  created_at: string;
  updated_at: string;
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
  static_assignments?: DHCPStaticAssignment[];
  outside_interface?: string;
  outside_address?: string;
  outside_gateway?: string;
  nat44_pool?: string;
  dnat_rules?: DNATRule[];
};

export type DHCPStaticAssignment = {
  mac: string;
  ip: string;
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
  in_vpp?: boolean;
  vpp_checked?: boolean;
  vpp_sw_if_index?: number;
  vpp_name?: string;
  vpp_up?: boolean;
  vpp_type?: string;
};

export type VNFLease = {
  client_id: string;
  address: string;
  hostname?: string;
  expires_at: string;
  active: boolean;
};

export type StatusResponse = {
  ready?: boolean;
  thin_pool?: {
    ready: boolean;
    state: string;
    detail?: string;
    vg?: string;
    name?: string;
  };
  vpp?: {
    ready: boolean;
    state?: string;
    detail?: string;
    socket?: string;
  };
};
