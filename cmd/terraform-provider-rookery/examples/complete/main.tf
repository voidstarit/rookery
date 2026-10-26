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
    vg             = "vg0"
    thin_pool      = "rookery"
    supports_templates = true
  }
}

resource "rookery_network" "app" {
  name      = "app"
  ipv4_cidr = "10.20.0.0/16"
  ipv6_cidr = "fd00:20::/64"
}

resource "rookery_interface" "app" {
  name       = "app-vm0"
  network_id = rookery_network.app.id
}

resource "rookery_disk" "root" {
  name               = "app-vm0-root"
  size_gb            = 20
  storage_backend_id = rookery_storage_backend.pool.id
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

resource "rookery_vnf" "dhcp" {
  name       = "app-dhcp"
  network_id = rookery_network.app.id
  type       = "dhcpv4"
  enabled    = true
  config_json = jsonencode({
    gateway     = "10.20.0.1"
    range_start = "10.20.0.100"
    range_end   = "10.20.0.200"
  })
}
