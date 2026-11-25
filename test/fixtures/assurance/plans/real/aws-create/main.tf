# The payments shape: a load balancer and an app instance reach a ledger database through security groups.

variable "extra_security_group_ids" {
  type    = list(string)
  default = []
}

resource "aws_vpc" "main" {
  cidr_block = "10.20.0.0/16"
}

resource "aws_subnet" "app" {
  vpc_id            = aws_vpc.main.id
  cidr_block        = "10.20.1.0/24"
  availability_zone = "eu-west-1a"
}

resource "aws_subnet" "data" {
  vpc_id            = aws_vpc.main.id
  cidr_block        = "10.20.2.0/24"
  availability_zone = "eu-west-1b"
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

  # Two inline ingress blocks that name different groups.
  ingress {
    from_port       = 5432
    to_port         = 5432
    protocol        = "tcp"
    security_groups = [aws_security_group.app.id]
  }

  ingress {
    from_port       = 6432
    to_port         = 6432
    protocol        = "tcp"
    security_groups = [aws_security_group.gateway.id]
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

resource "aws_lb" "public" {
  name               = "public"
  load_balancer_type = "application"
  security_groups    = [aws_security_group.gateway.id]
  subnets            = [aws_subnet.app.id, aws_subnet.data.id]
}

resource "aws_db_subnet_group" "ledger" {
  name       = "ledger"
  subnet_ids = [aws_subnet.app.id, aws_subnet.data.id]
}

resource "aws_db_instance" "ledger" {
  identifier                  = "ledger"
  engine                      = "postgres"
  instance_class              = "db.t3.micro"
  allocated_storage           = 20
  username                    = "ledger"
  manage_master_user_password = true
  db_subnet_group_name        = aws_db_subnet_group.ledger.name
  skip_final_snapshot         = true
  vpc_security_group_ids      = [aws_security_group.ledger_db.id]
}

# A database whose group list mixes a resource reference and a variable.
resource "aws_db_instance" "reports" {
  identifier                  = "reports"
  engine                      = "postgres"
  instance_class              = "db.t3.micro"
  allocated_storage           = 20
  username                    = "reports"
  manage_master_user_password = true
  db_subnet_group_name        = aws_db_subnet_group.ledger.name
  skip_final_snapshot         = true
  vpc_security_group_ids      = concat([aws_security_group.ledger_db.id], var.extra_security_group_ids)
}

# A group whose inline lists are set to empty lists: the plan knows it has no inline rules.
resource "aws_security_group" "locked" {
  name    = "locked"
  vpc_id  = aws_vpc.main.id
  ingress = []
  egress  = []
}

resource "aws_instance" "locked" {
  ami                    = "ami-12345678"
  instance_type          = "t3.micro"
  subnet_id              = aws_subnet.data.id
  private_ip             = "10.20.2.50"
  vpc_security_group_ids = [aws_security_group.locked.id]
}

# The app instance: its primary interface has the app group; a second interface carries the ops group.
resource "aws_instance" "app" {
  ami                    = "ami-12345678"
  instance_type          = "t3.micro"
  subnet_id              = aws_subnet.app.id
  private_ip             = "10.20.1.10"
  vpc_security_group_ids = [aws_security_group.app.id]
}

resource "aws_network_interface" "app_secondary" {
  subnet_id       = aws_subnet.app.id
  private_ips     = ["10.20.1.20"]
  security_groups = [aws_security_group.ops.id]
}

resource "aws_network_interface_attachment" "app_secondary" {
  instance_id          = aws_instance.app.id
  network_interface_id = aws_network_interface.app_secondary.id
  device_index         = 1
}

# An interface that gets an extra group through a separate attachment resource.
resource "aws_network_interface" "ops" {
  subnet_id       = aws_subnet.app.id
  private_ips     = ["10.20.1.30"]
  security_groups = [aws_security_group.app.id]
}

resource "aws_network_interface_sg_attachment" "ops" {
  security_group_id    = aws_security_group.ops.id
  network_interface_id = aws_network_interface.ops.id
}

# An instance whose primary interface is attached through the instance network_interface block.
resource "aws_network_interface" "bastion" {
  subnet_id       = aws_subnet.app.id
  private_ips     = ["10.20.1.40"]
  security_groups = [aws_security_group.ops.id]
}

resource "aws_instance" "bastion" {
  ami           = "ami-12345678"
  instance_type = "t3.micro"

  network_interface {
    network_interface_id = aws_network_interface.bastion.id
    device_index         = 0
  }
}

# A group with one inline ingress block that names one other group.
resource "aws_security_group" "cache" {
  name   = "cache"
  vpc_id = aws_vpc.main.id

  ingress {
    from_port       = 6379
    to_port         = 6379
    protocol        = "tcp"
    security_groups = [aws_security_group.app.id]
  }
}

resource "aws_elasticache_cluster" "cache" {
  cluster_id         = "cache"
  engine             = "redis"
  node_type          = "cache.t3.micro"
  num_cache_nodes    = 1
  security_group_ids = [aws_security_group.cache.id]
}
