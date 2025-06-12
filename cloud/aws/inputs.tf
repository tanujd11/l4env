variable "region" {
  default = "us-east-1"
}

variable "vpc_id" {
    type = string
}

variable "subnet_id" {
  type = string
}

variable "frr_ami_id" {
  description = "AMI ID for the instance"
  type        = string
}

variable "instance_type" {
  default = "t3.micro"
}

variable "ssh_key_name" {
  description = "Existing SSH key name (if any); leave empty to auto-create"
  type        = string
  default     = ""
}

variable "asg_ami_id" {
  description = "AMI ID for the ASG nodes"
  type        = string
}

variable "block_device_name" {
  description = "block device name for asg ami default storage"
  type        = string
}

variable "private_key_path" {
  description = "Path to your local private key file if using an existing SSH key pair (required if ssh_key_name is set)."
  type        = string
  default     = ""
}

variable "bgp_listen_range" {
  description = "The CIDR range for BGP listen command."
  type        = string
  default     = "172.31.0.0/16"
}

variable "mitm_vip" {
    type = string
}

variable "image_pull_secret_data" {
  description = "Secret data for image pull"
  type        = string
}
