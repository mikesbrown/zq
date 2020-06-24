package s3io

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
)

var ErrInvalidS3Path = errors.New("path is not a valid s3 location")

// uploader is an interface wrapper for s3manager.Uploader. This is only here
// for unit testing purposes.
type uploader interface {
	Upload(*s3manager.UploadInput, ...func(*s3manager.Uploader)) (*s3manager.UploadOutput, error)
}

func IsS3Path(path string) bool {
	_, _, err := parsePath(path)
	return err == nil
}

func parsePath(path string) (bucket, key string, err error) {
	var u *url.URL
	u, err = url.Parse(path)
	if err != nil {
		return
	}
	if u.Scheme != "s3" {
		err = ErrInvalidS3Path
	}
	bucket = u.Host
	key = u.Path
	return
}

type Writer struct {
	writer   *io.PipeWriter
	reader   *io.PipeReader
	uploader uploader
	bucket   string
	key      string
	once     sync.Once
	done     sync.WaitGroup
	err      error
}

func NewWriter(path string, cfg *aws.Config, options ...func(*s3manager.Uploader)) (*Writer, error) {
	bucket, key, err := parsePath(path)
	if err != nil {
		return nil, err
	}
	sess := session.Must(session.NewSession(cfg))
	uploader := s3manager.NewUploader(sess, options...)
	pr, pw := io.Pipe()
	return &Writer{
		bucket:   bucket,
		key:      key,
		writer:   pw,
		reader:   pr,
		uploader: uploader,
	}, nil
}

func (w *Writer) init() {
	w.done.Add(1)
	go func() {
		_, err := w.uploader.Upload(&s3manager.UploadInput{
			Bucket: aws.String(w.bucket),
			Key:    aws.String(w.key),
			Body:   w.reader,
		})
		w.err = err
		_ = w.reader.CloseWithError(err) // can ignore, return value will always be nil
		w.done.Done()
	}()
}

func (w *Writer) Write(b []byte) (int, error) {
	w.once.Do(w.init)
	return w.writer.Write(b)
}

func (w *Writer) Close() error {
	err := w.writer.Close()
	w.done.Wait()
	if err != nil {
		return err
	}
	return w.err
}

func RemoveAll(path string, cfg *aws.Config) error {
	bucket, key, err := parsePath(path)
	if err != nil {
		return err
	}
	sess := session.Must(session.NewSession(cfg))
	client := s3.New(sess)
	input := &s3.ListObjectsV2Input{
		Prefix: aws.String(key),
		Bucket: aws.String(bucket),
	}
	del := &s3.Delete{}
	err = client.ListObjectsV2Pages(input, func(out *s3.ListObjectsV2Output, lastPage bool) bool {
		for _, obj := range out.Contents {
			del.Objects = append(del.Objects, &s3.ObjectIdentifier{Key: obj.Key})
		}
		return true
	})
	_, err = client.DeleteObjects(&s3.DeleteObjectsInput{
		Bucket: &bucket,
		Delete: del,
	})
	return err
}

func Remove(path string, cfg *aws.Config) error {
	bucket, key, err := parsePath(path)
	if err != nil {
		return err
	}
	sess := session.Must(session.NewSession(cfg))
	client := s3.New(sess)
	_, err = client.DeleteObject(&s3.DeleteObjectInput{
		Key:    &key,
		Bucket: &bucket,
	})
	return err
}

func Rename(oldpath, newpath string, cfg *aws.Config) error {
	// ugh only way to do this is to copy and rename
	return nil
}

func Info(path string, cfg *aws.Config) (*s3.HeadObjectOutput, error) {
	bucket, key, err := parsePath(path)
	if err != nil {
		return nil, err
	}
	fmt.Println("Info", "path=", path, "bucket=", bucket, "key=", key)
	sess := session.Must(session.NewSession(cfg))
	client := s3.New(sess)
	return client.HeadObject(&s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
}

func Exists(path string, cfg *aws.Config) (bool, error) {
	_, err := Info(path, cfg)
	if err != nil {
		var reqerr awserr.RequestFailure
		if errors.As(err, &reqerr) && reqerr.StatusCode() == http.StatusNotFound {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
