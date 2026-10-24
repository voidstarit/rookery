terraform {
  required_providers {
    rookery = {
      source = "voidstarit/rookery"
    }
  }
}

provider "rookery" {
  endpoint    = "api.example.com:9443"
  ca_file     = "${path.module}/pki/ca.crt"
  cert_file   = "${path.module}/pki/client.crt"
  key_file    = "${path.module}/pki/client.key"
  server_name = "api.example.com"
}
