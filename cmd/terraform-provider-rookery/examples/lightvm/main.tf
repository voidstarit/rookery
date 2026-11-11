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
variable "kernel_source" { type = string }
variable "initramfs_source" { type = string }

provider "rookery" {
  endpoint       = var.endpoint
  ca_file        = var.ca_file
  cert_file      = var.cert_file
  key_file       = var.key_file
  upload_timeout = "30m"
}

resource "rookery_lightvm_kernel" "app" {
  name        = "app-vmlinuz"
  source_path = var.kernel_source
  source_hash = filemd5(var.kernel_source)
  labels      = ["app", "kernel"]
}

resource "rookery_lightvm_initramfs" "app" {
  name        = "app-initramfs"
  source_path = var.initramfs_source
  source_hash = filemd5(var.initramfs_source)
  labels      = ["app", "initramfs"]
}

resource "rookery_lightvm" "app" {
  name            = "app-01"
  description     = "Example container-backed LightVM"
  vcpus           = 2
  memory_mb       = 512
  console_enabled = true
  kernel_id       = rookery_lightvm_kernel.app.id
  initramfs_id    = rookery_lightvm_initramfs.app.id
  image           = "alpine:3.20"
  # Alternatively replace executable_path + executable_args with executable_script.
  executable_path = "/usr/local/bin/app"
  executable_args = ["serve"]
  append          = "console=hvc0 rdinit=/init"
  labels          = ["app", "lightvm"]

  env = [
    { name = "APP_ENV", value = "prod" },
  ]

  consoles = [
    { name = "serial" },
  ]

  virtiofs_shares = [
    {
      host_path = "/srv/app-data"
      tag       = "data"
      read_only = true
    },
  ]
}
