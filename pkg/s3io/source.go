package s3io

import (
	"errors"
	"io"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/brimsec/zq/pkg/iosrc"
)

var DefaultSource = &Source{}
var _ iosrc.Source = DefaultSource

type Source struct {
	Config *aws.Config
}

func (s *Source) NewWriter(uri iosrc.URI) (io.WriteCloser, error) {
	return NewWriter(uri.String(), s.Config)
}

func (s *Source) NewReader(uri iosrc.URI) (io.ReadCloser, error) {
	return nil, errors.New("method unsupported")
}

// XXX TODO
func (s *Source) Remove(uri iosrc.URI) error {
	return errors.New("method unsupported")
}

// XXX TODO
func (s *Source) RemoveAll(uri iosrc.URI) error {
	return errors.New("method unsupported")
}

// XXX TODO
func (s *Source) Rename(olduri, newuri iosrc.URI) error {
	return errors.New("method unsupported")
}

func (s *Source) Exists(uri iosrc.URI) (bool, error) {
	return Exists(uri.String(), s.Config)
}
