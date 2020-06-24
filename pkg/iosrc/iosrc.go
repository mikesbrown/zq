package iosrc

import (
	"errors"
	"io"
	"os"
	"sync"
)

const FileScheme = "file"

var DefaultRegistry = &Registry{
	schemes: map[string]Source{"file": DefaultFileSource},
}

type Source interface {
	NewReader(URI) (io.ReadCloser, error)
	NewWriter(URI) (io.WriteCloser, error)
	Remove(URI) error
	RemoveAll(URI) error
	// Rename(string, string) error
	// Exists returns true if the specified uri exists and an error is there
	// was an error finding this information.
	// XXX This should really be some interface akin to os.Stat, returning other
	// info about the bath, but for now this is fine.
	Exists(URI) (bool, error)
}

type DirMaker interface {
	MkdirAll(URI, os.FileMode) error
}

type AtomicWriter interface {
	AtomicWrite(URI, []byte) error
}

type Registry struct {
	mu      sync.RWMutex
	schemes map[string]Source
}

func (r *Registry) initWithLock() {
	if r.schemes == nil {
		r.schemes = map[string]Source{}
	}
}

func (r *Registry) Add(scheme string, loader Source) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.initWithLock()
	r.schemes[scheme] = loader
}

func (r *Registry) NewReader(uri URI) (io.ReadCloser, error) {
	s, err := r.Source(uri)
	if err != nil {
		return nil, err
	}
	return s.NewReader(uri)
}

func (r *Registry) NewWriter(uri URI) (io.WriteCloser, error) {
	s, err := r.Source(uri)
	if err != nil {
		return nil, err
	}
	return s.NewWriter(uri)
}

func (r *Registry) Source(uri URI) (Source, error) {
	scheme := getScheme(uri)
	r.mu.RLock()
	defer r.mu.RUnlock()
	r.initWithLock()
	loader, ok := r.schemes[scheme]
	if !ok {
		return nil, errors.New("unknown scheme")
	}
	return loader, nil
}

func (r *Registry) GetScheme(uri URI) (string, bool) {
	scheme := getScheme(uri)
	r.mu.RLock()
	defer r.mu.RUnlock()
	r.initWithLock()
	_, ok := r.schemes[scheme]
	return scheme, ok
}

func Register(scheme string, source Source) {
	DefaultRegistry.Add(scheme, source)
}

func NewReader(uri URI) (io.ReadCloser, error) {
	return DefaultRegistry.NewReader(uri)
}

func NewWriter(uri URI) (io.WriteCloser, error) {
	return DefaultRegistry.NewWriter(uri)
}

func getScheme(uri URI) string {
	if uri.Scheme == "" {
		return FileScheme
	}
	return uri.Scheme
}
