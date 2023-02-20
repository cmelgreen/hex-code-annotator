variable NAME {
    type = string
}

variable SIZE {
    type = number
    default = 128
}

variable HANDLER {
    type = string
    default = "main"
}

variable RUNTIME {
    type = string
    default = "go1.x"
}

variable TIMEOUT {
    type = number
    default = 60
}

variable DIR {
    type = string
}

variable COMMAND {
    type = string
    default = "make"
}

variable ZIP {
    type = string
    default = "lambda.zip"
}

variable ENV_VARS {
    type = map
    default = {}
}

variable LAYERS {
    type = list(string)
    default = []
}