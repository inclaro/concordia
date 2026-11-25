# The subset of the payments shape that LocalStack community can create: EC2 networking only.

resource "aws_vpc" "main" {
  cidr_block = "10.20.0.0/16"
}

resource "aws_subnet" "app" {
  vpc_id            = aws_vpc.main.id
  cidr_block        = "10.20.1.0/24"
  availability_zone = "eu-west-1a"
}

resource "aws_security_group" "gateway" {
  name   = "gateway"
  vpc_id = aws_vpc.main.id
}

resource "aws_security_group" "app" {
  name   = "app"
  vpc_id = aws_vpc.main.id

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_security_group" "ledger_db" {
  name   = "ledger-db"
  vpc_id = aws_vpc.main.id

  ingress {
    from_port       = 5432
    to_port         = 5432
    protocol        = "tcp"
    security_groups = [aws_security_group.app.id]
  }
}

resource "aws_security_group" "ops" {
  name   = "ops"
  vpc_id = aws_vpc.main.id
}

resource "aws_vpc_security_group_egress_rule" "gateway_out" {
  security_group_id = aws_security_group.gateway.id
  ip_protocol       = "-1"
  cidr_ipv4         = "0.0.0.0/0"
}

resource "aws_vpc_security_group_ingress_rule" "ledger_from_gateway" {
  security_group_id            = aws_security_group.ledger_db.id
  referenced_security_group_id = aws_security_group.gateway.id
  ip_protocol                  = "tcp"
  from_port                    = 5432
  to_port                      = 5432
  description                  = "temporary debug access"
}

resource "aws_vpc_security_group_ingress_rule" "ops_ssh" {
  security_group_id = aws_security_group.ops.id
  cidr_ipv4         = "10.20.0.0/16"
  ip_protocol       = "tcp"
  from_port         = 22
  to_port           = 22
}

resource "aws_security_group_rule" "ops_https" {
  type              = "ingress"
  security_group_id = aws_security_group.ops.id
  cidr_blocks       = ["10.20.1.0/24"]
  protocol          = "tcp"
  from_port         = 443
  to_port           = 443
}

resource "aws_network_interface" "gateway" {
  subnet_id       = aws_subnet.app.id
  private_ips     = ["10.20.1.5"]
  security_groups = [aws_security_group.gateway.id]
}

resource "aws_network_interface" "ledger" {
  subnet_id       = aws_subnet.app.id
  private_ips     = ["10.20.1.6"]
  security_groups = [aws_security_group.ledger_db.id]
}

resource "aws_instance" "app" {
  ami                    = "ami-df5de72bdb3b"
  instance_type          = "m5.large"
  subnet_id              = aws_subnet.app.id
  private_ip             = "10.20.1.10"
  vpc_security_group_ids = [aws_security_group.app.id]
}

# LocalStack community cannot create aws_network_interface_attachment (the provider waits with a
# DescribeNetworkInterfaces filter that it does not implement), so the interface attaches itself.
resource "aws_network_interface" "app_secondary" {
  subnet_id       = aws_subnet.app.id
  private_ips     = ["10.20.1.20"]
  security_groups = [aws_security_group.ops.id]

  attachment {
    instance     = aws_instance.app.id
    device_index = 1
  }
}

resource "aws_network_interface" "ops" {
  subnet_id       = aws_subnet.app.id
  private_ips     = ["10.20.1.30"]
  security_groups = [aws_security_group.app.id]
}

resource "aws_network_interface_sg_attachment" "ops" {
  security_group_id    = aws_security_group.ops.id
  network_interface_id = aws_network_interface.ops.id
}

resource "aws_network_interface" "bastion" {
  subnet_id       = aws_subnet.app.id
  private_ips     = ["10.20.1.40"]
  security_groups = [aws_security_group.ops.id]
}

resource "aws_instance" "bastion" {
  ami           = "ami-df5de72bdb3b"
  instance_type = "m5.large"

  network_interface {
    network_interface_id = aws_network_interface.bastion.id
    device_index         = 0
  }
}
