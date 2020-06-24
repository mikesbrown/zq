package iosrc

import (
	"io"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/brimsec/zq/pkg/fs"
)

var DefaultFileSource = &FileSource{Perm: 0666}
var _ DirMaker = DefaultFileSource
var _ AtomicWriter = DefaultFileSource

type FileSource struct {
	Perm os.FileMode
}

func (f *FileSource) NewReader(uri URI) (io.ReadCloser, error) {
	return fs.Open(uri.Filepath())
}

func (s *FileSource) NewWriter(uri URI) (io.WriteCloser, error) {
	return fs.OpenFile(uri.Filepath(), os.O_RDWR|os.O_CREATE|os.O_TRUNC, s.Perm)
}

func (s *FileSource) MkdirAll(uri URI, perm os.FileMode) error {
	return os.MkdirAll(uri.Filepath(), perm)
}

func (s *FileSource) Remove(uri URI) error {
	return os.Remove(uri.Filepath())
}

func (s *FileSource) RemoveAll(uri URI) error {
	return os.RemoveAll(uri.Filepath())
}

func (s *FileSource) Rename(olduri, newuri URI) error {
	return os.Rename(olduri.Filepath(), newuri.Filepath())
}

func (s *FileSource) AtomicWrite(uri URI, data []byte) error {
	p := uri.Filepath()
	f, err := ioutil.TempFile(filepath.Dir(p), "."+filepath.Base(p)+".*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	err = f.Close()
	if err != nil {
		os.Remove(tmp)
	}
	err = os.Rename(tmp, p)
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

func (s *FileSource) Exists(uri URI) (bool, error) {
	_, err := os.Stat(uri.Filepath())
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
