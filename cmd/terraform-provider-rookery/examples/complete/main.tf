terraform {
  required_providers {
    rookery = {
      source = "voidstarit/rookery"
    }
  }
}

variable "endpoint" { type = string }
variable "ca_file" { type = string }
variable "cert_file" { type = string }
variable "key_file" { type = string }

provider "rookery" {
  endpoint  = var.endpoint
  ca_file   = var.ca_file
  cert_file = var.cert_file
  key_file  = var.key_file
}

resource "rookery_storage_backend" "pool" {
  name   = "pool"
  title  = "Primary LVM thin pool"
  driver = "lvm-thin"

  config = {
    vg                 = "vg0"
    thin_pool          = "rookery"
    supports_templates = true
    preparation_script = "/opt/rookery/scripts/lvm-thin-snapshot-disk.sh"
  }
}

resource "rookery_network" "app" {
  name      = "app"
  ipv4_cidr = "10.20.0.0/16"
  ipv6_cidr = "fd00:20::/64"
}

resource "rookery_network" "edge" {
  name      = "edge"
  ipv4_cidr = "10.21.0.0/16"
  ipv6_cidr = "fd00:21::/64"
}

resource "rookery_network_pipe" "app_edge" {
  network_a_id = rookery_network.app.id
  network_b_id = rookery_network.edge.id
}

resource "rookery_interface" "app" {
  name       = "app-vm0"
  network_id = rookery_network.app.id
}

resource "rookery_host_tap" "app" {
  network_id   = rookery_network.app.id
  name         = "hosttap0"
  ipv4_address = "10.20.0.20/24"
}

resource "rookery_vpp_interface_addresses" "app_vm0" {
  name = rookery_interface.app.name
  addresses = [
    "10.20.0.50/16",
    "fd00:20::50/64",
  ]
}

resource "rookery_vpp_interface_addresses" "pipe_a" {
  name = rookery_network_pipe.app_edge.endpoint_a_name
  addresses = [
    "10.20.0.60/16",
    "fd00:20::60/64",
  ]
}

resource "rookery_disk" "root" {
  name               = "app-vm0-root"
  size_gb            = 20
  storage_backend_id = rookery_storage_backend.pool.id
}

resource "rookery_disk" "template" {
  name                 = "debian-13-template"
  size_gb              = 10
  storage_backend_id   = rookery_storage_backend.pool.id
  is_template          = true
  provisioning_script  = "/opt/rookery/scripts/template-provision.sh"
  provisioning_timeout = "5m"
}

resource "rookery_disk" "web" {
  name               = "web-01"
  storage_backend_id = rookery_storage_backend.pool.id
  template_disk_id   = rookery_disk.template.id
  provisioning_json = jsonencode({
    hostname    = "web-01"
    timezone    = "UTC"
    username    = "deploy"
    resize_disk = true
  })
}

resource "rookery_vm" "app" {
  name          = "app-vm0"
  vcpus         = 2
  memory_mb     = 2048
  disk_ids      = [rookery_disk.root.id]
  interface_ids = [rookery_interface.app.id]
}

resource "rookery_acl" "app" {
  name = "app"
  rules = [
    {
      action      = "permit"
      protocol    = "tcp"
      source      = "10.20.0.0/16"
      destination = "10.20.0.0/16"
    },
    {
      action      = "deny"
      protocol    = "any"
      source      = "0.0.0.0/0"
      destination = "0.0.0.0/0"
    },
  ]
}

resource "rookery_route_table" "app" {
  name     = "app-vrf"
  table_id = 200
  ipv6     = false
}

resource "rookery_route" "app" {
  route_table_id = rookery_route_table.app.id
  ipv6           = false
  prefix         = "10.20.0.0/16"
  next_hop       = "10.20.0.1"
  description    = "Application subnet"
  preference     = 10
}

resource "rookery_vpp_interface_route_tables" "app_tap" {
  name          = rookery_host_tap.app.name
  ipv4_table_id = rookery_route_table.app.table_id
  ipv6_table_id = 0
}

resource "rookery_startup_script" "vpp" {
  kind    = "vpp"
  enabled = true
  script  = "vppctl set nat workers 0"
}

resource "rookery_vnf" "dns" {
  name       = "app-dns"
  network_id = rookery_network.app.id
  type       = "dns"
  enabled    = true
  config_json = jsonencode({
    dns_records = [
      {
        name  = "router.lab.test"
        type  = "A"
        value = "10.20.0.1"
        ttl   = 300
      }
    ]
    dns_forward = true
  })
}

resource "rookery_vnf" "dhcp" {
  name       = "app-dhcp"
  network_id = rookery_network.app.id
  type       = "dhcpv4"
  enabled    = true
  config_json = jsonencode({
    gateway     = "10.20.0.1"
    range_start = "10.20.0.100"
    range_end   = "10.20.0.200"
    dns         = "10.20.0.2"
    dns_vnf_id  = rookery_vnf.dns.id
    dns_domain  = "lab.test"
    dns_ttl     = 300
  })
}
