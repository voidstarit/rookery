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
variable "kernel_id" { type = number }
variable "initramfs_id" { type = number }
variable "storage_backend_id" { type = number }
variable "network_id" { type = number }

provider "rookery" {
  endpoint  = var.endpoint
  ca_file   = var.ca_file
  cert_file = var.cert_file
  key_file  = var.key_file
}

resource "rookery_lightvm_template" "web" {
  name        = "web"
  description = "Example image-backed web fleet"

  spec = {
    vcpus        = 2
    memory_mb    = 2048
    kernel_id    = var.kernel_id
    initramfs_id = var.initramfs_id
    image        = "registry.example.com/web@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
    append       = "console=hvc0 rdinit=/init"

    executable_script = <<-EOT
      #!/bin/sh
      exec /usr/local/bin/web
    EOT

    env = [
      { name = "APP_ENV", value = "prod" },
    ]

    disks = [
      {
        storage_backend_id = var.storage_backend_id
        size_gb            = 20
        serial             = "data"
      },
    ]

    interfaces = [
      { network_id = var.network_id },
    ]
  }
}

resource "rookery_lightvm_group" "web" {
  name                = "web-group"
  template_version_id = rookery_lightvm_template.web.current_version_id
  desired_count       = 3
  wait_for_ready      = true
  timeout             = "2h"
  force_destroy       = false
  destroy_timeout     = "6h"
}
