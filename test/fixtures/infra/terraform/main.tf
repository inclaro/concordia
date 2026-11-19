variable "db_password" {
  type      = string
  sensitive = true
}

variable "region" {
  type    = string
  default = "eu-west-1"
}

module "network" {
  source = "./modules/network"
  region = var.region
}

resource "aws_db_instance" "payments" {
  identifier = "payments"
  password   = var.db_password
  subnet_id  = module.network.subnet_id
}

output "db_endpoint" {
  value = aws_db_instance.payments.endpoint
}
