package iosrc

import (
	"net/url"
	"path"
	"path/filepath"
)

type URI struct {
	url.URL
}

// ParseURI parses the path using `url.Parse`. If the Scheme of the provided
// path is an empty, Scheme is set to file.
func ParseURI(path string) (URI, error) {
	u, err := url.Parse(path)
	if err != nil {
		return URI{}, err
	}
	if u.Scheme == "" {
		u.Scheme = FileScheme
	}
	return URI{*u}, nil
}

func (p URI) AppendPath(elem ...string) URI {
	p.Path = path.Join(append([]string{p.Path}, elem...)...)
	return p
}

func (p URI) Filepath() string {
	return filepath.FromSlash(p.Path)
}

func (p URI) String() string {
	return (&p.URL).String()
}
