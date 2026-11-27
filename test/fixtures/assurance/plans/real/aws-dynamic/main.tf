# Dynamic blocks: does the JSON configuration show a dynamic block, and is the planned list known?

variable "rules" {
  type = list(object({ port = number, cidr = string }))
  default = [
    { port = 22, cidr = "10.20.0.0/16" },
    { port = 443, cidr = "10.20.1.0/24" },
  ]
}

variable "no_rules" {
  type    = list(object({ port = number, cidr = string }))
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

# No inline blocks at all.
resource "aws_security_group" "plain" {
  name   = "plain"
  vpc_id = aws_vpc.main.id
}

# (a) dynamic ingress over a known, non-empty list.
resource "aws_security_group" "dyn_known" {
  name   = "dyn-known"
  vpc_id = aws_vpc.main.id

  dynamic "ingress" {
    for_each = var.rules
    content {
      from_port   = ingress.value.port
      to_port     = ingress.value.port
      protocol    = "tcp"
      cidr_blocks = [ingress.value.cidr]
    }
  }
}

# (b) dynamic ingress over a list that is unknown until apply.
resource "aws_security_group" "dyn_unknown" {
  name   = "dyn-unknown"
  vpc_id = aws_vpc.main.id

  dynamic "ingress" {
    for_each = toset([aws_vpc.main.id])
    content {
      from_port       = 5432
      to_port         = 5432
      protocol        = "tcp"
      security_groups = [aws_security_group.plain.id]
    }
  }
}

# (c) dynamic ingress over a known, empty list.
resource "aws_security_group" "dyn_empty" {
  name   = "dyn-empty"
  vpc_id = aws_vpc.main.id

  dynamic "ingress" {
    for_each = var.no_rules
    content {
      from_port   = ingress.value.port
      to_port     = ingress.value.port
      protocol    = "tcp"
      cidr_blocks = [ingress.value.cidr]
    }
  }
}

resource "aws_network_interface" "extra" {
  subnet_id       = aws_subnet.app.id
  security_groups = [aws_security_group.plain.id]
}

# Dynamic network_interface over a known list.
resource "aws_instance" "dyn_nic" {
  ami           = "ami-12345678"
  instance_type = "t3.micro"

  dynamic "network_interface" {
    for_each = [aws_network_interface.extra.id]
    content {
      network_interface_id = network_interface.value
      device_index         = 0
    }
  }
}

# Dynamic network_interface over a list that is unknown until apply.
resource "aws_instance" "dyn_nic_unknown" {
  ami           = "ami-12345678"
  instance_type = "t3.micro"

  dynamic "network_interface" {
    for_each = toset([aws_network_interface.extra.id])
    content {
      network_interface_id = network_interface.value
      device_index         = 0
    }
  }
}

# (d) dynamic ingress over a list whose length is unknown until apply.
resource "aws_security_group" "dyn_unknown_length" {
  name   = "dyn-unknown-length"
  vpc_id = aws_vpc.main.id

  dynamic "ingress" {
    for_each = aws_vpc.main.id == "" ? [] : var.rules
    content {
      from_port   = ingress.value.port
      to_port     = ingress.value.port
      protocol    = "tcp"
      cidr_blocks = [ingress.value.cidr]
    }
  }
}

# Dynamic network_interface over a list whose length is unknown until apply.
resource "aws_instance" "dyn_nic_unknown_length" {
  ami           = "ami-12345678"
  instance_type = "t3.micro"

  dynamic "network_interface" {
    for_each = aws_vpc.main.id == "" ? [] : [aws_network_interface.extra.id]
    content {
      network_interface_id = network_interface.value
      device_index         = 0
    }
  }
}

# No network_interface block.
resource "aws_instance" "plain" {
  ami           = "ami-12345678"
  instance_type = "t3.micro"
  subnet_id     = aws_subnet.app.id
}
