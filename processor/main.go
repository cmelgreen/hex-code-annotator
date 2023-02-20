package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	"io"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"

	"github.com/daddye/vips"
	"github.com/mccutchen/palettor"
)

type imageData struct {
	Key       string        `json:"key"`
	MessageId string        `json:"itemIdentifier"`
	Reader    io.ReadSeeker `json:",omitempty"`
	Colors    []string      `json:"hexcolors"`
}

type messageError struct {
	MessageId string
	Err error
}

type itemIdentifier struct {
	ItemIdentifier string `json:"itemIdentifier"`
}

type batchItemFailures struct {
	BatchItemFailures []itemIdentifier `json:"batchItemIdentifier"`
}

func (m messageError) Error() string {
    return fmt.Sprintf("Error occured: message: %s: %s", m.MessageId, m.Err)
}


func startWorkers[F func(A) (B, error), A, B any](numWorkers int, inChan chan A, errChan chan error, workFunc F) chan B {
	outChan := make(chan B)

	go func(outChan chan B) {
		wg := &sync.WaitGroup{}

		for i := 0; i < numWorkers; i++ {
			wg.Add(1)

			go func(inChan chan A, outChan chan B, errChan chan error) {
				defer wg.Done()

				for inValue := range inChan {
					outValue, err := workFunc(inValue)
					if err != nil{
						errChan <- err
					} else {
						outChan <- outValue
					}
				}
			}(inChan, outChan, errChan)
		}

		wg.Wait()
		close(outChan)

	}(outChan)

	return outChan
}

func handleErrors(errChan chan error) batchItemFailures {
	messageErrors := batchItemFailures{}

	for err := range errChan {
		messageErrors.BatchItemFailures = append(messageErrors.BatchItemFailures, itemIdentifier{err.(messageError).MessageId})
		fmt.Printf("Error num: %d: %s", len(messageErrors.BatchItemFailures), err)
	}

	return messageErrors
}

func getImage(downloader *s3manager.Downloader, bucket string, key string) (imageData, error) {
	buf := aws.NewWriteAtBuffer([]byte{})

	_, err := downloader.Download(buf,
		&s3.GetObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		})
	if err != nil {
		return imageData{}, fmt.Errorf("error getting image: %s: %s", key, err)
	}

	return imageData{Key: key, Reader: bytes.NewReader(buf.Bytes())}, nil
}

func resizeImage(options vips.Options, image imageData) (imageData, error) {
	image.Reader.Seek(0, 0)
	inBuf, err := ioutil.ReadAll(image.Reader)
	if err != nil {
		return imageData{}, fmt.Errorf("error reading image: %s: %s", image.Key, err)
	}

	outBuf, err := vips.Resize(inBuf, options)
	if err != nil {
		return imageData{}, fmt.Errorf("error resizing image: %s: %s", image.Key, err)
	}

	image.Reader = bytes.NewReader(outBuf)
	return image, nil
}

func rgbToHex(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("%x%x%x", normalize(r), normalize(g), normalize(b))
}

func normalize(i uint32) uint32 {
	return uint32(float64(i) * float64(255) / float64(65535))
}

func generatePalette(numColors int, maxIterations int, img imageData) (imageData, error) {
	img.Reader.Seek(0, 0)
	image, _, err := image.Decode(img.Reader)
	if err != nil {
		return imageData{}, fmt.Errorf("error reading image for palette: %s: %s", img.Key, err)
	}

	palette, err := palettor.Extract(numColors, maxIterations, image)
	if err != nil {
		return imageData{}, fmt.Errorf("error generating palette %s", err)
	}

	colors := make([]string, numColors)
	for i, color := range palette.Colors() {
		colors[i] = rgbToHex(color)
	}

	img.Colors = colors

	return img, nil
}

func prefixFirstTwoChars(key string) string {
	keySplit := strings.Split(key, "/")
	return fmt.Sprintf("%s/%s/%s", keySplit[0], keySplit[1][:2], keySplit[1][2:])
}

func postImage(uploader *s3manager.Uploader, bucket string, image imageData) (imageData, error) {
	image.Reader.Seek(0, 0)
	_, err := uploader.Upload(&s3manager.UploadInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(prefixFirstTwoChars(image.Key)),
		Body:   image.Reader,
	})
	if err != nil {
		err = fmt.Errorf("error posting image: %s: %s", image.Key, err)
	}
	return image, err
}

func postImageMetadata(uploader *s3manager.Uploader, bucket string, image imageData) (imageData, error) {
	buf := bytes.Buffer{}

	err := json.NewEncoder(&buf).Encode(image)
	if err != nil {
		return image, fmt.Errorf("error encoding metadata for image: %s: %s", image.Key, err)
	}

	_, err = uploader.Upload(&s3manager.UploadInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(prefixFirstTwoChars(strings.TrimSuffix(image.Key, ".jpg") + ".json")),
		Body:   bytes.NewReader(buf.Bytes()),
	})
	if err != nil {
		return image, fmt.Errorf("error uploading metadata for image: %s: %s", image.Key, err)
	}

	return image, nil
}

func HandleRequest(ctx context.Context, sqsEvent events.SQSEvent) (string, error) {
	bucket := os.Getenv("DATA_BUCKET")
	outputBucket := os.Getenv("OUTPUT_BUCKET")
	region := os.Getenv("AWS_REGION")
	numColors, _ := strconv.Atoi(os.Getenv("NUM_COLORS"))
	maxIterations, _ := strconv.Atoi(os.Getenv("MAX_ITERATIONS"))
	numWorkers, _ := strconv.Atoi(os.Getenv("NUM_WORKERS"))
	numRecords := len(sqsEvent.Records)

	// if numWorkers > numRecords {
	// 	numWorkers = numRecords
	// }

	fmt.Println("numWorkers: ", numWorkers)
	fmt.Println("numRecords: ", numRecords)

	if bucket == "" || outputBucket == "" || region == "" || numWorkers <= 0 || numColors == 0 || maxIterations == 0 {
		return "Necessary env vars not set", fmt.Errorf("necessary env vars not set")
	}

	sess, err := session.NewSession()
	if err != nil {
		fmt.Println("Error creating session ", err)
		return "Failure", err
	}

	svc := s3.New(sess, &aws.Config{
		Region:      aws.String(region),
		Credentials: credentials.AnonymousCredentials,
	})

	downloader := s3manager.NewDownloaderWithClient(svc)
	uploader := s3manager.NewUploader(sess)

	vipsOptions := vips.Options{
		Width:        256,
		Height:       256,
		Crop:         false,
		Extend:       vips.EXTEND_WHITE,
		Interpolator: vips.BILINEAR,
		Gravity:      vips.CENTRE,
		Quality:      70,
	}

	fmt.Println("starting")

	keyChan := make(chan string, len(sqsEvent.Records))
	errChan := make(chan error)
	errResultChan := make(chan batchItemFailures)

	go handleErrors(errChan)

	getImageChan := startWorkers(numRecords, keyChan, errChan, func(key string) (imageData, error) {
		return getImage(downloader, bucket, key)
	})

	resizeImageChan := startWorkers(numWorkers, getImageChan, errChan, func(image imageData) (imageData, error) {
		return resizeImage(vipsOptions, image)
	})

	postImageChan := startWorkers(numWorkers, resizeImageChan, errChan, func(image imageData) (imageData, error) {
		return postImage(uploader, outputBucket, image)
	})

	generatePaletteChan := startWorkers(numWorkers, postImageChan, errChan, func(image imageData) (imageData, error) {
		return generatePalette(numColors, maxIterations, image)
	})

	postImageMetaDataChan := startWorkers(numWorkers, generatePaletteChan, errChan, func(image imageData) (imageData, error) {
		return postImageMetadata(uploader, outputBucket, image)
	})

	for _, record := range sqsEvent.Records {
		keyChan <- record.Body
	}
	close(keyChan)

	fmt.Println("waiting on postImageMetaDatChan")
	for range postImageMetaDataChan {
	}

	fmt.Println("waiting on finalErrResults")
	close(errChan)
	finalErrResults := <- errResultChan

	result, err := json.Marshal(finalErrResults)

	return string(result), err
}

func main() {
	lambda.Start(HandleRequest)
}
