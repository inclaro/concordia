variable "region" {
  type = string
}

resource "aws_subnet" "payments" {
  availability_zone = "${var.region}a"
}

output "subnet_id" {
  value = aws_subnet.payments.id
}
