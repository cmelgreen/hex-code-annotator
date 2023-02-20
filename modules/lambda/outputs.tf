output lambda_arn {
    value = aws_lambda_function.lambda.arn
}

output role_arn {
    value = aws_iam_role.lambda_role.arn
}

output role_name {
    value = aws_iam_role.lambda_role.name
}

output timeout {
    value = var.TIMEOUT
}