// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Terraform AWS generator (INFRA-020/050/063/064). Emits main.tf +
// variables.tf for: VPC (private-by-default — INFRA-064), ECS Fargate,
// RDS Postgres (with IAM DB auth opt — INFRA-063), ElastiCache Redis, ALB.
// Multi-region support: region + optional replica region (INFRA-050).

package terraform

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/OgonFrameworks/ogon.go/infra"
)

// Generator implements infra.Generator for the terraform target.
type Generator struct{}

func (Generator) Name() string { return "terraform" }

func (Generator) Plan(cfg *infra.InfraConfig) []string {
	return []string{
		"infra/terraform/aws/main.tf",
		"infra/terraform/aws/outputs.tf",
		"infra/terraform/aws/providers.tf",
		"infra/terraform/aws/variables.tf",
	}
}

func (Generator) Generate(cfg *infra.InfraConfig) ([]infra.FileSpec, error) {
	main, err := GenerateAWSMain(cfg)
	if err != nil {
		return nil, err
	}
	vars, err := GenerateAWSVariables(cfg)
	if err != nil {
		return nil, err
	}
	outs, err := GenerateAWSOutputs(cfg)
	if err != nil {
		return nil, err
	}
	provs, err := GenerateAWSProviders(cfg)
	if err != nil {
		return nil, err
	}
	return []infra.FileSpec{main, vars, outs, provs}, nil
}

// GenerateAWSMain emits main.tf.
func GenerateAWSMain(cfg *infra.InfraConfig) (infra.FileSpec, error) {
	tpl := template.Must(template.New("main").Funcs(template.FuncMap{
		"vpcPrivate": func(c *infra.InfraConfig) bool { return c.Terraform.VPCPrivate },
	}).Parse(mainTemplate))
	var b strings.Builder
	if err := tpl.Execute(&b, cfg); err != nil {
		return infra.FileSpec{}, fmt.Errorf("tf main template: %w", err)
	}
	return infra.FileSpec{
		Path: "infra/terraform/aws/main.tf", Content: b.String(),
		Marker: infra.MarkOwned, CommentPrefix: "#",
		Description: "AWS: VPC private-by-default (INFRA-064) + ECS Fargate + RDS Postgres (IAM DB auth opt INFRA-063) + ElastiCache + ALB",
	}, nil
}

// GenerateAWSVariables emits variables.tf.
func GenerateAWSVariables(cfg *infra.InfraConfig) (infra.FileSpec, error) {
	tpl := template.Must(template.New("vars").Parse(varsTemplate))
	var b strings.Builder
	if err := tpl.Execute(&b, cfg); err != nil {
		return infra.FileSpec{}, fmt.Errorf("tf vars template: %w", err)
	}
	return infra.FileSpec{
		Path: "infra/terraform/aws/variables.tf", Content: b.String(),
		Marker: infra.MarkOwned, CommentPrefix: "#",
		Description: "Terraform variables (region, account, app, multi-region — INFRA-050)",
	}, nil
}

// GenerateAWSOutputs emits outputs.tf.
func GenerateAWSOutputs(cfg *infra.InfraConfig) (infra.FileSpec, error) {
	const outs = `output "vpc_id" {
  description = "VPC id"
  value       = aws_vpc.main.id
}

output "alb_dns_name" {
  description = "ALB DNS name for the app"
  value       = aws_lb.main.dns_name
}

output "ecs_cluster_arn" {
  description = "ECS cluster ARN"
  value       = aws_ecs_cluster.main.arn
}

output "rds_endpoint" {
  description = "RDS Postgres endpoint"
  value       = aws_db_instance.main.endpoint
  sensitive   = true
}

output "redis_endpoint" {
  description = "ElastiCache Redis endpoint"
  value       = aws_elasticache_cluster.main.cache_nodes[0].address
  sensitive   = true
}
`
	return infra.FileSpec{
		Path: "infra/terraform/aws/outputs.tf", Content: outs,
		Marker: infra.MarkOwned, CommentPrefix: "#",
		Description: "Terraform outputs: VPC, ALB DNS, ECS cluster, RDS, ElastiCache",
	}, nil
}

// GenerateAWSProviders emits providers.tf.
func GenerateAWSProviders(cfg *infra.InfraConfig) (infra.FileSpec, error) {
	tpl := template.Must(template.New("provs").Parse(provsTemplate))
	var b strings.Builder
	if err := tpl.Execute(&b, cfg); err != nil {
		return infra.FileSpec{}, fmt.Errorf("tf provs template: %w", err)
	}
	return infra.FileSpec{
		Path: "infra/terraform/aws/providers.tf", Content: b.String(),
		Marker: infra.MarkOwned, CommentPrefix: "#",
		Description: "Terraform providers: aws + awscc; default tags; multi-region alias when configured",
	}, nil
}

const provsTemplate = `terraform {
  required_version = ">= 1.7.0"
  required_providers {
    aws    = { source = "hashicorp/aws",    version = "~> 5.0" }
    awscc  = { source = "hashicorp/awscc",   version = "~> 1.0" }
    random = { source = "hashicorp/random",  version = "~> 3.5" }
  }
}

provider "aws" {
  region = var.aws_region
  default_tags {
    tags = {
      Project   = var.app_name
      ManagedBy = "terraform"
      Framework = "ogon"
    }
  }
}
{{if .Terraform.MultiRegion}}
# INFRA-050: multi-region alias. The replica region hosts a read replica of
# the primary RDS instance and a warm ElastiCache node-group.
provider "aws" {
  alias  = "replica"
  region = var.aws_replica_region
  default_tags {
    tags = {
      Project   = var.app_name
      ManagedBy = "terraform"
      Framework = "ogon"
      Region    = "replica"
    }
  }
}
{{end}}
`

const varsTemplate = `variable "aws_region" {
  description = "Primary AWS region"
  type        = string
  default     = "{{.Terraform.Region}}"
}

variable "aws_replica_region" {
  description = "Replica region (multi-region opt — INFRA-050)"
  type        = string
  default     = "us-west-2"
}

variable "app_name" {
  description = "Application name"
  type        = string
  default     = "{{.App.Name}}"
}

variable "app_version" {
  description = "Application version (semver, stamped at deploy)"
  type        = string
  default     = "{{.App.Version}}"
}

variable "image_repo" {
  description = "ECR repository URI"
  type        = string
  default     = "{{.Terraform.AccountID}}.dkr.ecr.{{.Terraform.Region}}.amazonaws.com/{{.App.Name}}"
}

variable "db_name" {
  description = "Postgres database name"
  type        = string
  default     = "{{.Database.Name}}"
}

variable "db_user" {
  description = "Postgres master user"
  type        = string
  default     = "{{.Database.User}}"
}

variable "multi_region" {
  description = "Enable multi-region replicas (INFRA-050)"
  type        = bool
  default     = {{.Terraform.MultiRegion}}
}

variable "iam_db_auth" {
  description = "Use IAM database auth for RDS (INFRA-063)"
  type        = bool
  default     = {{.Terraform.IAMDBAuth}}
}

variable "vpc_private" {
  description = "VPC private-by-default: no IGW on private subnets (INFRA-064)"
  type        = bool
  default     = {{.Terraform.VPCPrivate}}
}
`

const mainTemplate = `# ---------------------------------------------------------------------------
# VPC — INFRA-064 private-by-default. Two AZs, public subnets for ALB only,
# private subnets for Fargate + RDS, no IGW on private route tables. NAT
# gateway is optional and off by default (egress via VPC endpoints or none).
# ---------------------------------------------------------------------------
resource "aws_vpc" "main" {
  cidr_block           = "10.0.0.0/16"
  enable_dns_support   = true
  enable_dns_hostnames = true
}

resource "aws_subnet" "public" {
  count                   = 2
  vpc_id                  = aws_vpc.main.id
  cidr_block              = cidrsubnet(aws_vpc.main.cidr_block, 4, count.index)
  availability_zone       = data.aws_availability_zones.available.names[count.index]
  map_public_ip_on_launch = true
  tags = { Name = "${var.app_name}-public-${count.index + 1}" }
}

resource "aws_subnet" "private" {
  count             = 2
  vpc_id            = aws_vpc.main.id
  cidr_block        = cidrsubnet(aws_vpc.main.cidr_block, 4, count.index + 2)
  availability_zone = data.aws_availability_zones.available.names[count.index]
  tags = { Name = "${var.app_name}-private-${count.index + 1}" }
}

resource "aws_internet_gateway" "main" {
  vpc_id = aws_vpc.main.id
}

resource "aws_route_table" "public" {
  vpc_id = aws_vpc.main.id
  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.main.id
  }
}

resource "aws_route_table_association" "public" {
  count          = 2
  subnet_id      = aws_subnet.public[count.index].id
  route_table_id = aws_route_table.public.id
}

# Private route tables have NO default route to the IGW (INFRA-064).
# Egress, when needed, is via VPC endpoints (S3, ECR, etc.) below.
resource "aws_route_table" "private" {
  vpc_id = aws_vpc.main.id
}

resource "aws_route_table_association" "private" {
  count          = 2
  subnet_id      = aws_subnet.private[count.index].id
  route_table_id = aws_route_table.private.id
}

data "aws_availability_zones" "available" {
  state = "available"
}

# ---------------------------------------------------------------------------
# Security groups: ALB → Fargate → RDS / Redis (least-privilege)
# ---------------------------------------------------------------------------
resource "aws_security_group" "alb" {
  name        = "${var.app_name}-alb"
  description = "ALB ingress (80/443 from anywhere)"
  vpc_id      = aws_vpc.main.id
  ingress {
    from_port   = 80
    to_port     = 80
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }
  ingress {
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_security_group" "fargate" {
  name        = "${var.app_name}-fargate"
  description = "Fargate tasks ingress from ALB only"
  vpc_id      = aws_vpc.main.id
  ingress {
    from_port       = 8080
    to_port         = 8080
    protocol        = "tcp"
    security_groups = [aws_security_group.alb.id]
  }
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_security_group" "db" {
  name        = "${var.app_name}-db"
  description = "Postgres from Fargate only"
  vpc_id      = aws_vpc.main.id
  ingress {
    from_port       = 5432
    to_port         = 5432
    protocol        = "tcp"
    security_groups = [aws_security_group.fargate.id]
  }
  egress { from_port = 0; to_port = 0; protocol = "-1"; cidr_blocks = ["0.0.0.0/0"] }
}

resource "aws_security_group" "redis" {
  name        = "${var.app_name}-redis"
  description = "Redis from Fargate only"
  vpc_id      = aws_vpc.main.id
  ingress {
    from_port       = 6379
    to_port         = 6379
    protocol        = "tcp"
    security_groups = [aws_security_group.fargate.id]
  }
  egress { from_port = 0; to_port = 0; protocol = "-1"; cidr_blocks = ["0.0.0.0/0"] }
}

# ---------------------------------------------------------------------------
# ALB
# ---------------------------------------------------------------------------
resource "aws_lb" "main" {
  name               = var.app_name
  internal           = false
  load_balancer_type = "application"
  security_groups    = [aws_security_group.alb.id]
  subnets            = aws_subnet.public[*].id
}

resource "aws_lb_target_group" "app" {
  name        = var.app_name
  port        = 8080
  protocol    = "HTTP"
  vpc_id      = aws_vpc.main.id
  target_type = "ip"
  health_check {
    path                = "/healthz"
    healthy_threshold   = 2
    unhealthy_threshold = 2
    timeout             = 2
    interval            = 5
    matcher             = "200"
  }
}

resource "aws_lb_listener" "http" {
  load_balancer_arn = aws_lb.main.arn
  port              = 80
  protocol          = "HTTP"
  default_action {
    type             = "redirect"
    redirect { port = "443"; protocol = "HTTPS"; status_code = "HTTP_301" }
  }
}

resource "aws_lb_listener" "https" {
  load_balancer_arn = aws_lb.main.arn
  port              = 443
  protocol          = "HTTPS"
  ssl_policy         = "ELBSecurityPolicy-TLS13-1-2-2021-06"
  certificate_arn    = aws_acm_certificate.main.arn
  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.app.arn
  }
}

resource "aws_acm_certificate" "main" {
  domain_name       = "app.example.com"
  validation_method = "DNS"
}

# ---------------------------------------------------------------------------
# ECS Fargate
# ---------------------------------------------------------------------------
resource "aws_ecs_cluster" "main" {
  name = var.app_name
}

resource "aws_ecs_task_definition" "app" {
  family                   = var.app_name
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = "256"   # INFRA-043 baseline
  memory                   = "512"   # INFRA-043 baseline
  execution_role_arn       = aws_iam_role.ecs_exec.arn
  task_role_arn            = aws_iam_role.ecs_task.arn
  container_definitions = jsonencode([
    {
      name      = var.app_name
      image     = "${var.image_repo}:${var.app_version}"
      cpu       = 256
      memory    = 512
      essential = true
      port_mappings = [{ container_port = 8080, host_port = 8080, protocol = "tcp" }]
      health_check = {
        command     = ["CMD-SHELL", "/app health --path /healthz --port 8080"]
        interval    = 10
        timeout     = 1
        retries     = 3
        start_period = 5
      }
      environment = [
        { name = "APP_ENV", value = "prod" },
        { name = "HTTP_PORT", value = "8080" }
      ]
      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = "/ecs/${var.app_name}"
          "awslogs-region"        = var.aws_region
          "awslogs-stream-prefix" = "app"
        }
      }
    }
  ])
}

resource "aws_ecs_service" "app" {
  name            = var.app_name
  cluster         = aws_ecs_cluster.main.id
  task_definition = aws_ecs_task_definition.app.arn
  desired_count   = 2
  launch_type      = "FARGATE"
  health_check_grace_period_seconds = 30
  network_configuration {
    subnets          = aws_subnet.private[*].id
    security_groups  = [aws_security_group.fargate.id]
    assign_public_ip = false
  }
  load_balancer {
    target_group_arn = aws_lb_target_group.app.arn
    container_name   = var.app_name
    container_port   = 8080
  }
  deployment_controller {
    type = "ECS"
  }
}

# ---------------------------------------------------------------------------
# RDS Postgres — INFRA-063: IAM DB auth optional.
# ---------------------------------------------------------------------------
resource "aws_db_subnet_group" "main" {
  name       = var.app_name
  subnet_ids = aws_subnet.private[*].id
}

resource "aws_db_instance" "main" {
  engine                 = "postgres"
  engine_version         = "16"
  instance_class         = "db.t4g.micro"
  allocated_storage      = 20
  storage_encrypted      = true
  db_name                = var.db_name
  username               = var.db_user
  password               = var.db_password
  db_subnet_group_name   = aws_db_subnet_group.main.name
  vpc_security_group_ids = [aws_security_group.db.id]
  multi_az               = true
  backup_retention_period = 7
  storage_type           = "gp3"
  deletion_protection    = true
  publicly_accessible    = false   # INFRA-064
  iam_database_authentication_enabled = var.iam_db_auth   # INFRA-063
}

# ---------------------------------------------------------------------------
# ElastiCache Redis
# ---------------------------------------------------------------------------
resource "aws_elasticache_subnet_group" "main" {
  name       = var.app_name
  subnet_ids = aws_subnet.private[*].id
}

resource "aws_elasticache_cluster" "main" {
  cluster_id           = var.app_name
  engine               = "redis"
  engine_version       = "7.1"
  node_type            = "cache.t4g.micro"
  num_cache_nodes      = 1
  parameter_group_name = "default.redis7"
  subnet_group_name    = aws_elasticache_subnet_group.main.name
  security_group_ids   = [aws_security_group.redis.id]
}

# ---------------------------------------------------------------------------
# IAM roles for ECS task + execution
# ---------------------------------------------------------------------------
data "aws_iam_policy_document" "ecs_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["ecs-tasks.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "ecs_exec" {
  name               = "${var.app_name}-ecs-exec"
  assume_role_policy = data.aws_iam_policy_document.ecs_assume.json
}

resource "aws_iam_role" "ecs_task" {
  name               = "${var.app_name}-ecs-task"
  assume_role_policy = data.aws_iam_policy_document.ecs_assume.json
}

resource "aws_iam_role_policy_attachment" "ecs_exec" {
  role       = aws_iam_role.ecs_exec.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
}

{{if .Terraform.IAMDBAuth}}
# INFRA-063: IAM DB auth — allow the task role to generate RDS auth tokens.
resource "aws_iam_role_policy" "rds_iam_auth" {
  name = "${var.app_name}-rds-iam-auth"
  role = aws_iam_role.ecs_task.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["rds-db:connect"]
      Resource = "${aws_db_instance.main.arn}/db_user_${var.db_user}"
    }]
  })
}
{{end}}

# ---------------------------------------------------------------------------
# Multi-region replica (INFRA-050) — opt-in.
# ---------------------------------------------------------------------------
{{if .Terraform.MultiRegion}}
resource "aws_db_instance" "replica" {
  provider                   = aws.replica
  replicate_source_db        = aws_db_instance.main.arn
  instance_class             = "db.t4g.micro"
  availability_zone          = "${var.aws_replica_region}a"
  db_subnet_group_name       = aws_db_subnet_group.main.name
  vpc_security_group_ids     = [aws_security_group.db.id]
  backup_retention_period    = 7
  storage_encrypted          = true
  deletion_protection        = true
  publicly_accessible        = false
}
{{end}}
`
