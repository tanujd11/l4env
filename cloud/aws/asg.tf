resource "random_pet" "asg_suffix" {
  length    = 2
  separator = "-"
}

resource "aws_security_group" "asg_self" {
  name        = "asg-self-group-${random_pet.asg_suffix.id}"
  description = "Allow all traffic within this security group"
  vpc_id      = aws_vpc.main.id

  # Allow all traffic from same SG
  ingress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    self        = true
    description = "Allow all inbound traffic from instances with same SG"
  }

  # Allow SSH from anywhere
  ingress {
    from_port   = 22
    to_port     = 22
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
    description = "Allow SSH from anywhere"
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
    description = "Allow all outbound traffic"
  }

  tags = {
    Name = "asg-self-group-${random_pet.asg_suffix.id}"
  }
}

resource "aws_launch_template" "asg" {
  name_prefix   = "asg-template-"
  image_id      = var.asg_ami_id
  instance_type = var.instance_type
  key_name      = local.final_key_name
  
  block_device_mappings {
    device_name = var.block_device_name

    ebs {
      delete_on_termination = true
      volume_size           = 50
      volume_type           = "gp3"
    }
  }

  vpc_security_group_ids = [aws_security_group.asg_self.id]
  iam_instance_profile {
    name = aws_iam_instance_profile.asg_profile.name
  }

  user_data = base64encode(<<-EOF
    #!/bin/bash
    set -e

    # Install awscli if not already present
    if ! command -v aws &> /dev/null; then
      apt-get update -y
      apt-get install -y awscli
    fi

    INSTANCE_ID=$(curl -s http://169.254.169.254/latest/meta-data/instance-id)
    REGION=$(curl -s http://169.254.169.254/latest/dynamic/instance-identity/document | grep region | awk -F\" '{print $4}')
    aws ec2 modify-instance-attribute --instance-id "$INSTANCE_ID" --no-source-dest-check --region "$REGION"
  EOF
  )

  tag_specifications {
    resource_type = "instance"
    tags = {
      Name = "asg-node"
    }
  }
}

resource "aws_autoscaling_group" "asg" {
  name                = "asg-group-${random_pet.asg_suffix.id}"
  min_size            = 4
  max_size            = 4
  desired_capacity    = 4
  vpc_zone_identifier = [aws_subnet.public.id]
  wait_for_capacity_timeout    = "10m"   # Wait up to 10 minutes for desired_capacity
  health_check_type            = "EC2"
  force_delete                 = true

  launch_template {
    id      = aws_launch_template.asg.id
    version = "$Latest"
  }

  tag {
    key                 = "Name"
    value               = "asg-node"
    propagate_at_launch = true
  }

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_iam_policy" "disable_src_dst_check" {
  name        = "DisableSourceDestCheck-${random_pet.asg_suffix.id}"
  description = "Allow disabling source/destination check on own instance"
  policy = jsonencode({
    Version = "2012-10-17",
    Statement = [
      {
        Effect = "Allow",
        Action = [
          "ec2:ModifyInstanceAttribute",
          "ec2:DescribeInstances"
        ],
        Resource = "*"
      }
    ]
  })
}

resource "aws_iam_role" "ec2_asg_role" {
  name = "asg-ec2-role-${random_pet.asg_suffix.id}"

  assume_role_policy = jsonencode({
    Version = "2012-10-17",
    Statement = [
      {
        Effect = "Allow",
        Principal = {
          Service = "ec2.amazonaws.com"
        },
        Action = "sts:AssumeRole"
      }
    ]
  })
}

resource "aws_iam_role_policy_attachment" "asg_role_attach_policy" {
  role       = aws_iam_role.ec2_asg_role.name
  policy_arn = aws_iam_policy.disable_src_dst_check.arn
}

resource "aws_iam_instance_profile" "asg_profile" {
  name = "asg-ec2-profile-${random_pet.asg_suffix.id}"
  role = aws_iam_role.ec2_asg_role.name
}

data "external" "asg_private_ips" {
  program = ["${path.module}/wait-for-asg-ips.sh"]

  query = {
    asg_name       = aws_autoscaling_group.asg.name
    expected_count = 4
    region         = var.region
  }
}
