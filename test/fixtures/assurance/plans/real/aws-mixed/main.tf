# A static inline block and a dynamic inline block in one list: what does the configuration show?

resource "aws_vpc" "main" {
  cidr_block = "10.20.0.0/16"
}

resource "aws_security_group" "a" {
  name   = "a"
  vpc_id = aws_vpc.main.id
}

resource "aws_security_group" "b" {
  name   = "b"
  vpc_id = aws_vpc.main.id
}

# Only static blocks: one names group a, one names group b.
resource "aws_security_group" "static_two" {
  name   = "static-two"
  vpc_id = aws_vpc.main.id

  ingress {
    from_port       = 5432
    to_port         = 5432
    protocol        = "tcp"
    security_groups = [aws_security_group.a.id]
  }

  ingress {
    from_port       = 6432
    to_port         = 6432
    protocol        = "tcp"
    security_groups = [aws_security_group.b.id]
  }
}

# One static block names group a; one dynamic block names group b.
resource "aws_security_group" "mixed" {
  name   = "mixed"
  vpc_id = aws_vpc.main.id

  ingress {
    from_port       = 5432
    to_port         = 5432
    protocol        = "tcp"
    security_groups = [aws_security_group.a.id]
  }

  dynamic "ingress" {
    for_each = [6432]
    content {
      from_port       = ingress.value
      to_port         = ingress.value
      protocol        = "tcp"
      security_groups = [aws_security_group.b.id]
    }
  }
}

# Only a dynamic block names group b.
resource "aws_security_group" "dynamic_only" {
  name   = "dynamic-only"
  vpc_id = aws_vpc.main.id

  dynamic "ingress" {
    for_each = [6432]
    content {
      from_port       = ingress.value
      to_port         = ingress.value
      protocol        = "tcp"
      security_groups = [aws_security_group.b.id]
    }
  }
}

# One static block names group a, with a CIDR block written as a constant.
resource "aws_security_group" "static_one" {
  name   = "static-one"
  vpc_id = aws_vpc.main.id

  ingress {
    from_port       = 5432
    to_port         = 5432
    protocol        = "tcp"
    security_groups = [aws_security_group.a.id]
  }

  ingress {
    from_port   = 22
    to_port     = 22
    protocol    = "tcp"
    cidr_blocks = ["10.20.0.0/16"]
  }
}
