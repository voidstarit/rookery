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

data "rookery_status" "current" {}

data "rookery_storage_backend" "pool" {
  name = "pool"
}

data "rookery_disk" "root" {
  name = "app-vm0-root"
}

data "rookery_media" "installer" {
  name = "debian-13"
}

data "rookery_network" "app" {
  name = "app"
}

data "rookery_network" "edge" {
  name = "edge"
}

data "rookery_network_pipe" "app_edge" {
  network_a_id = data.rookery_network.app.id
  network_b_id = data.rookery_network.edge.id
}

data "rookery_interface" "app" {
  name = "app-vm0"
}

data "rookery_host_tap" "app" {
  name = "hosttap0"
}

data "rookery_vpp_interface_addresses" "app" {
  name = "app-vm0"
}

data "rookery_vpp_interface_route_tables" "app_tap" {
  name = "hosttap0"
}

data "rookery_startup_script" "vpp" {
  kind = "vpp"
}

data "rookery_cgroup_settings" "host" {}

data "rookery_acl" "app" {
  name = "app"
}

data "rookery_route_table" "app" {
  name = "app-vrf"
}

data "rookery_route" "default_v4" {
  route_table_id = 0
  ipv6           = false
  prefix         = "0.0.0.0/0"
}

data "rookery_vnf" "dhcp" {
  name = "app-dhcp"
}

data "rookery_vm" "app" {
  name = "app-vm0"
}

data "rookery_host_interface" "uplink" {
  nic = "enp1s0"
}

output "network_route_table" {
  value = data.rookery_network.app.route_table_id
}

output "vm_status" {
  value = data.rookery_vm.app.status
}

output "cgroup_status" {
  value = data.rookery_cgroup_settings.host.configured
}

output "vm_labels" {
  value = data.rookery_vm.app.labels
}
