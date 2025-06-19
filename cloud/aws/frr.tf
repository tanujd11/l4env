# If ssh_key_name is not set, generate one
resource "tls_private_key" "generated" {
  count      = var.ssh_key_name == "" ? 1 : 0
  algorithm  = "RSA"
  rsa_bits   = 4096
}

resource "aws_key_pair" "generated" {
  count      = var.ssh_key_name == "" ? 1 : 0
  key_name   = "auto-generated-key-${random_id.suffix[0].hex}"
  public_key = tls_private_key.generated[0].public_key_openssh
}

resource "random_id" "suffix" {
  count = var.ssh_key_name == "" ? 1 : 0
  byte_length = 4
}

# Pick the key name to use
locals {
  final_key_name = var.ssh_key_name != "" ? var.ssh_key_name : aws_key_pair.generated[0].key_name
  private_key    = var.ssh_key_name != "" ? file(var.private_key_path) : tls_private_key.generated[0].private_key_pem
  private_key_path_for_copy = var.ssh_key_name != "" ? var.private_key_path : "${path.module}/auto-generated-key.pem"
  config_yaml = templatefile("${path.module}/config.yaml.tpl", {
    bgpPeerAddress      = aws_instance.frr_router.private_ip # this gets the new FRR private IP
    imagePullSecretData = var.image_pull_secret_data         # this comes from tfvars or similar
  })
}

resource "aws_instance" "frr_router" {
  ami                         = var.frr_ami_id
  instance_type               = var.instance_type
  subnet_id                   = var.subnet_id
  vpc_security_group_ids      = [aws_security_group.asg_self.id]
  key_name                    = local.final_key_name
  associate_public_ip_address = true
  source_dest_check           = false

  user_data = <<-EOF
    #!/bin/bash
    set -e
    export DEBIAN_FRONTEND=noninteractive
    apt-get update -y
    apt-get install -y frr

    sed -i 's/bgpd=no/bgpd=yes/' /etc/frr/daemons

    systemctl restart frr

    vtysh << EOC
    configure terminal
    router bgp 64514
     no bgp ebgp-requires-policy
     neighbor CILIUM-NODES peer-group
     neighbor CILIUM-NODES remote-as 64512
     bgp listen range  ${var.bgp_listen_range} peer-group CILIUM-NODES
    end
    write
    exit
    EOC

    systemctl restart frr
    MAIN_IF=$(ip -o -4 route show to default | awk '{print $5}' | cut -d/ -f1)
    INSTANCE_IP=$(ip -o -4 addr show $${MAIN_IF} | awk '{print $4}' | cut -d/ -f1)
    ip tunnel add mitm-tunnel mode ipip remote ${var.mitm_vip} local $${INSTANCE_IP}
    ip link set mitm-tunnel up

    # 1. Cleanup existing state
    iptables -t mangle -F
    iptables -t nat -F
    ip rule del fwmark 42 table fwdtun 2>/dev/null || true
    ip route flush table fwdtun 2>/dev/null || true

    # 2. Enable IP forwarding
    echo 1 | tee /proc/sys/net/ipv4/ip_forward
    sysctl -w net.ipv4.ip_forward=1

    # 3. Ensure custom table exists
    grep -q fwdtun /etc/iproute2/rt_tables || echo "200 fwdtun" | tee -a /etc/iproute2/rt_tables

    # 4. Mark forwarded traffic (exclude EC2-origin and EC2-destined)
    iptables -t mangle -A PREROUTING \
    -m addrtype ! --src-type LOCAL \
    -m addrtype ! --dst-type LOCAL \
    -j MARK --set-mark 42

    # 5. Policy routing for marked packets
    ip rule add fwmark 42 table fwdtun
    ip route add default dev mitm-tunnel table fwdtun
  EOF

  provisioner "file" {
    source      = "${path.module}/bin/l4env_amd64"
    destination = "/home/ubuntu/l4env_amd64"

    connection {
      type        = "ssh"
      user        = "ubuntu"
      private_key = local.private_key
      host        = self.public_ip
    }
  }

  provisioner "file" {
    source      = local.private_key_path_for_copy
    destination = "/home/ubuntu/ssh_key.pem"

    connection {
        type        = "ssh"
        user        = "ubuntu"
        private_key = local.private_key
        host        = self.public_ip
    }
  }

  provisioner "remote-exec" {
    inline = [
        "chown ubuntu:ubuntu /home/ubuntu/l4env_amd64",
        "chmod +x /home/ubuntu/l4env_amd64"
    ]
    connection {
      type        = "ssh"
      user        = "ubuntu"
      private_key = local.private_key
      host        = self.public_ip
    }
  }

  tags = {
    Name = "frr-router-${random_pet.asg_suffix.id}"
  }
}

resource "null_resource" "frr_config_upload" {
  triggers = {
    instance_id = aws_instance.frr_router.id   # Ensures this waits until instance is ready
    private_ip  = aws_instance.frr_router.private_ip
    image_pull_secret_data = var.image_pull_secret_data
  }

  provisioner "file" {
    content     = templatefile("${path.module}/config.yaml.tpl", {
      bgpPeerAddress      = aws_instance.frr_router.private_ip
      imagePullSecretData = var.image_pull_secret_data
    })
    destination = "/home/ubuntu/config.yaml"

    connection {
      type        = "ssh"
      user        = "ubuntu"
      private_key = local.private_key
      host        = aws_instance.frr_router.public_ip
    }
  }
}

# Write the generated private key to a local file (for your own SSH use)
resource "local_file" "private_key" {
  count    = var.ssh_key_name == "" ? 1 : 0
  content  = tls_private_key.generated[0].private_key_pem
  filename = "${path.module}/auto-generated-key.pem"
  file_permission = "0600"
}
