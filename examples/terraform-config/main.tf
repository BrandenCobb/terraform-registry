# Example Terraform configuration using providers from the registry network mirror

terraform {
  required_version = ">= 1.5.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "6.31.0"
    }
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "3.0.1"
    }
    kubectl = {
      source  = "gavinbunney/kubectl"
      version = "1.19.0"
    }
  }
}

provider "aws" {
  region = "us-gov-west-1"
}

# Example: Get current AWS account
data "aws_caller_identity" "current" {}

output "account_id" {
  value = data.aws_caller_identity.current.account_id
}

output "caller_arn" {
  value = data.aws_caller_identity.current.arn
}
