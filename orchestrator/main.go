package main

import (
	"context"
	"fmt"
	"os"
    "sync"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/sqs"

	"github.com/aws/aws-lambda-go/lambda"
)

func makePrefixListTwoHexChars(prefix string) []string {
	charset := "0123456789abcdef"
	charsetTwoChars := make([]string, 0)

    for _, char1 := range charset {
        for _, char2 := range charset {
			charsetTwoChars = append(charsetTwoChars, prefix + "/" + string(char1) + string(char2))
        }
    }

	return charsetTwoChars
}

func handleCount(countChan, finalCountChan chan int) {
    count := 0
    
    for x := range countChan {
        count += x
		if (count+1) % 10000 == 0 {
	    		fmt.Println(count+1)
		}
    }
    
    finalCountChan <- count
    close(finalCountChan)

}

func handleFinalCount(finalCountChan chan int) int {
    count := 0
	for finalCount := range finalCountChan {
		count = finalCount
	}
    return count
}

func listAllPrefixes(svc *s3.S3, bucket string, prefixList []string, listObjectChan chan *s3.ListObjectsV2Output) *sync.WaitGroup {
    var bucketListWG = &sync.WaitGroup{}
    
    for _, prefix := range prefixList {
            bucketListWG.Add(1)    
            go listPrefix(svc, &bucket, prefix, listObjectChan, bucketListWG)
    }
    
	return bucketListWG
}

func listPrefix(svc *s3.S3, bucket *string, prefix string, listObjectChan chan *s3.ListObjectsV2Output, bucketReadWG *sync.WaitGroup) { 
    defer bucketReadWG.Done()

    err := svc.ListObjectsV2Pages(&s3.ListObjectsV2Input{
        Bucket: bucket,
        Prefix: &prefix,
    }, func(page *s3.ListObjectsV2Output, _ bool) (shouldContinue bool) {
     listObjectChan <- page
        return true
    })
    
    if err != nil {
        fmt.Println("failed to list objects: ", err)
        return
    }
}

func processAllListObjectOutputs(processListObject func(*s3.Object) error, numWorkers int, countChan chan int, listObjectChan chan *s3.ListObjectsV2Output) *sync.WaitGroup {
	var processListObjectWG = &sync.WaitGroup{}

    for i := 0; i < numWorkers; i++ {
        processListObjectWG.Add(1)
        go processListObjectOutput(processListObject, countChan, listObjectChan, processListObjectWG)
    }

	return processListObjectWG
}

func processListObjectOutput(processListObject func(*s3.Object) error, countChan chan int, listObjectChan chan *s3.ListObjectsV2Output, processListObjectWG *sync.WaitGroup) {
    defer processListObjectWG.Done()
    
    for page := range listObjectChan {
        for _, obj := range page.Contents {
			err := processListObject(obj)
			if err != nil {
				fmt.Println("failed to process object: ", err)
			} else {
				countChan <- 1
			}
        }
    }
}

type OrchestratorEvent struct {
	Name string `json:"name"`
}

func HandleRequest(ctx context.Context, event OrchestratorEvent) (string, error) {
    bucket := os.Getenv("DATA_BUCKET")
    region := os.Getenv("AWS_REGION")
    prefixSplit := os.Getenv("SPLIT")
	sqsURL := os.Getenv("SQS_URL")
	
    sess, err := session.NewSession()
    if err != nil {
        fmt.Println("Error creating session ", err)
		return "Failure", err
    }

	svcS3 := s3.New(sess, &aws.Config{
        Region: aws.String(region),
        Credentials: credentials.AnonymousCredentials,
    })

	svcSQS := sqs.New(sess)

	prefixList := makePrefixListTwoHexChars(prefixSplit)

	processListObject := func(obj *s3.Object) error {
		_, err := svcSQS.SendMessage(&sqs.SendMessageInput{
			MessageBody: obj.Key,
			QueueUrl:    &sqsURL,
		})
		return err
	}

	countChan := make(chan int)
    finalCountChan := make(chan int)
    listObjectChan := make(chan *s3.ListObjectsV2Output)

    fmt.Println("Starting...")

    go handleCount(countChan, finalCountChan)
    
	objectProcessWG := processAllListObjectOutputs(processListObject, len(prefixList), countChan, listObjectChan)
	bucketListWG := listAllPrefixes(svcS3, bucket, prefixList, listObjectChan)

	bucketListWG.Wait()
	close(listObjectChan)
    objectProcessWG.Wait()
    close(countChan)

    fmt.Println("Ending...")

	finalCount := handleFinalCount(finalCountChan)

    fmt.Println("Total objects processed: ", finalCount)

	return "Success", nil
}

func main() {
	lambda.Start(HandleRequest)
}