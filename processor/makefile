build:
	go build -o main *.go
	zip lambda.zip main
	aws lambda update-function-code \
    --function-name hex-processor \
    --zip-file fileb://lambda.zip