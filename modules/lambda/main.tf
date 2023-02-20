resource aws_lambda_function lambda {
    function_name    = var.NAME
    filename         = "${var.DIR}/${var.ZIP}"
    role             = aws_iam_role.lambda_role.arn
    handler          = var.HANDLER
    runtime          = var.RUNTIME
    timeout          = var.TIMEOUT
    memory_size      = var.SIZE
    layers           = var.LAYERS

    environment {
        variables = var.ENV_VARS
    }
}

resource aws_iam_role lambda_role {
  name               = "${var.NAME}-lambda-assume-role"
  assume_role_policy = data.aws_iam_policy_document.lambda_assume_role.json

  lifecycle {
    create_before_destroy = true
  }
}

data aws_iam_policy_document lambda_assume_role {
  version = "2012-10-17"

  statement {
    sid     = "LambdaAssumeRole"
    effect  = "Allow"
    actions = ["sts:AssumeRole"]

    principals {
      identifiers = ["lambda.amazonaws.com"]
      type        = "Service"
    }
  }
}