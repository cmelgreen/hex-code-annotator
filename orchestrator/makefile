build:
	go build -o main *.go
	zip lambda.zip main
	aws lambda update-function-code \
    --function-name hex-orchestrator \
    --zip-file fileb://lambda.zip