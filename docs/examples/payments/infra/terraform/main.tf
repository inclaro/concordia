# The plan in infra/plans is a hand-written `terraform show -json` document of a plan of this configuration
# against the deployed estate. In that plan, only aws_vpc_security_group_ingress_rule.ledger_from_gateway is new.

terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

provider "aws" {
  region = "eu-west-1"
}

resource "aws_vpc" "main" {
  cidr_block = "10.16.0.0/16"
}

# The application subnets use a secondary range of the VPC. The rule ledger_from_app admits this range.
resource "aws_vpc_ipv4_cidr_block_association" "app" {
  vpc_id     = aws_vpc.main.id
  cidr_block = "10.20.0.0/16"
}

resource "aws_subnet" "public" {
  vpc_id     = aws_vpc.main.id
  cidr_block = "10.16.1.0/24"
}

resource "aws_security_group" "gateway" {
  name        = "payments-gateway"
  description = "Public load balancer of the payments platform"
  vpc_id      = aws_vpc.main.id
}

resource "aws_security_group" "ledger_db" {
  name        = "ledger-db"
  description = "Ledger database"
  vpc_id      = aws_vpc.main.id
}

resource "aws_lb" "public" {
  name               = "payments-public"
  load_balancer_type = "network"
  internal           = false
  subnets            = [aws_subnet.public.id]
  security_groups    = [aws_security_group.gateway.id]
}

resource "aws_db_instance" "ledger" {
  identifier                  = "ledger"
  engine                      = "postgres"
  instance_class              = "db.r6g.large"
  allocated_storage           = 100
  username                    = "ledger"
  manage_master_user_password = true
  vpc_security_group_ids      = [aws_security_group.ledger_db.id]
}

resource "aws_vpc_security_group_egress_rule" "gateway_out" {
  security_group_id = aws_security_group.gateway.id
  description       = "Load balancer to its targets"
  cidr_ipv4         = "0.0.0.0/0"
  ip_protocol       = "-1"
}

resource "aws_vpc_security_group_ingress_rule" "ledger_from_app" {
  security_group_id = aws_security_group.ledger_db.id
  description       = "Application subnets to the ledger database"
  cidr_ipv4         = "10.20.0.0/16"
  ip_protocol       = "tcp"
  from_port         = 5432
  to_port           = 5432
}

resource "aws_vpc_security_group_ingress_rule" "ledger_from_gateway" {
  security_group_id            = aws_security_group.ledger_db.id
  description                  = "temporary debug access"
  referenced_security_group_id = aws_security_group.gateway.id
  ip_protocol                  = "tcp"
  from_port                    = 5432
  to_port                      = 5432
}
