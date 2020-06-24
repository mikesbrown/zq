package emitter

import (
	"io"
	"os"

	"github.com/brimsec/zq/pkg/bufwriter"
	"github.com/brimsec/zq/pkg/iosrc"
	"github.com/brimsec/zq/zio"
	"github.com/brimsec/zq/zio/detector"
)

type noClose struct {
	io.Writer
}

func (*noClose) Close() error {
	return nil
}

func NewFile(path string, flags *zio.WriterFlags) (*zio.Writer, error) {
	uri, err := iosrc.ParseURI(path)
	if err != nil {
		return nil, err
	}
	return NewFileWithSource(uri, flags, iosrc.DefaultRegistry)
}

func NewFileWithSource(path iosrc.URI, flags *zio.WriterFlags, source *iosrc.Registry) (*zio.Writer, error) {
	var err error
	var f io.WriteCloser
	// XXX
	if path == iosrc.URI{} {
		// Don't close stdout in case we live inside something
		// here that runs multiple instances of this to stdout.
		f = &noClose{os.Stdout}
	} else {
		f, err = source.NewWriter(path)
		if err != nil {
			return nil, err
		}
	}
	// On close, zio.Writer.Close(), the zng WriteFlusher will be flushed
	// then the bufwriter will closed (which will flush it's internal buffer
	// then close the file)
	w := detector.LookupWriter(bufwriter.New(f), flags)
	if w == nil {
		return nil, unknownFormat(flags.Format)
	}
	return w, nil
}
