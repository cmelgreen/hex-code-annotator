terraform {
  cloud {
    organization = "cmelgreen"

    workspaces {
      name = "hex-dev"
    }
  }
}

provider "aws" {
  region = "us-east-1"
}

locals {
  ENV_VARS = {
    DATA_BUCKET    = "open-images-dataset"
    NUM_WORKERS    = 100
    NUM_COLORS     = 8
    MAX_ITERATIONS = 50
    SPLIT          = "challenge2018"
    SQS_URL        = aws_sqs_queue.hex_image_queue.id
    OUTPUT_BUCKET  = aws_s3_bucket.output_bucket.bucket
  }
}

resource "aws_s3_bucket" "output_bucket" {
  bucket = "hex-output"
}

module "hex_orchestrator_lambda" {
  source = "./modules/lambda"

  NAME    = "hex-orchestrator"
  SIZE    = 10240
  TIMEOUT = 30
  DIR     = "./orchestrator"

  ENV_VARS = local.ENV_VARS
}

module "hex_processor_lambda" {
  source = "./modules/lambda"

  NAME    = "hex-processor"
  SIZE    = 10240
  TIMEOUT = 40
  DIR     = "./processor"
  LAYERS  = [data.aws_lambda_layer_version.vips.arn]

  ENV_VARS = local.ENV_VARS
}

data "aws_lambda_layer_version" "vips" {
  layer_name = "vips"
}

resource "aws_iam_role_policy_attachment" "orchestrator_lambda_policies" {
  role       = module.hex_orchestrator_lambda.role_name
  policy_arn = aws_iam_policy.lambda_sqs_s3_logging.arn
}

resource "aws_iam_role_policy_attachment" "processor_lambda_policies" {
  role       = module.hex_processor_lambda.role_name
  policy_arn = aws_iam_policy.lambda_sqs_s3_logging.arn
}

resource "aws_iam_policy" "lambda_sqs_s3_logging" {
  name   = "lambda-sqs-s3"
  policy = data.aws_iam_policy_document.lambda_sqs_s3_logging.json
}

data "aws_iam_policy_document" "lambda_sqs_s3_logging" {
  statement {
    sid       = "SQSFull"
    effect    = "Allow"
    actions   = ["sqs:*"]
    resources = ["*"]
  }

  statement {
    sid       = "S3Full"
    effect    = "Allow"
    actions   = ["s3:*"]
    resources = ["*"]
  }

  statement {
    sid       = "S3OutputBucket"
    effect    = "Allow"
    actions   = ["s3:*"]
    resources = [aws_s3_bucket.output_bucket.arn]
  }

  statement {
    sid       = "CloudWatchFull"
    effect    = "Allow"
    actions   = ["logs:*"]
    resources = ["*"]
  }
}

resource "aws_sqs_queue" "hex_image_queue" {
  name                       = "hex-image-queue"
  visibility_timeout_seconds = 40
}

resource "aws_lambda_event_source_mapping" "hex_image_event_mapping" {
  event_source_arn                   = aws_sqs_queue.hex_image_queue.arn
  function_name                      = module.hex_processor_lambda.lambda_arn
  batch_size                         = 600
  maximum_batching_window_in_seconds = module.hex_processor_lambda.timeout

  depends_on = [data.aws_iam_policy_document.lambda_sqs_s3_logging]
}
