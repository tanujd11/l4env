output "ssh_private_key_path" {
  value = var.ssh_key_name == "" ? local_file.private_key[0].filename : var.private_key_path
  description = "Path to the private key you can use to ssh to the instance"
}

output "asg_name" {
  value = aws_autoscaling_group.asg.name
  description = "The name of the Auto Scaling Group"
}

output "frr_instance_public_ip" {
  value       = aws_instance.frr_router.public_ip
  description = "Public IP address of the FRR router instance"
}

output "frr_instance_private_ip" {
  value       = aws_instance.frr_router.private_ip
  description = "Private IP address of the FRR router instance"
}

output "asg_instance_private_ips" {
  value = jsondecode(data.external.asg_private_ips.result.private_ips)
}
