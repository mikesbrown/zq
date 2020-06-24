package zdx

import (
	"fmt"
	"io"

	"github.com/brimsec/zq/pkg/iosource"
	"github.com/brimsec/zq/zio/zngio"
	"github.com/brimsec/zq/zng/resolver"
)

const (
	FrameSize = 32 * 1024
)

// Reader implements zbuf.Reader, io.ReadSeeker, and io.Closer.
type Reader struct {
	zngio.Seeker
	reader io.ReadCloser
}

// NewReader returns a Reader ready to read a zdx.
// Close() should be called when done.  This embeds a bnzgio.Seeker so
// Seek() may be called on this Reader.  Any call to Seek() must be to
// an offset that begins a new zng stream (e.g., beginning of file or
// the data immediately following an end-of-stream code)
func NewReader(zctx *resolver.Context, path string) (*Reader, error) {
	return newReader(zctx, path, 0)
}

func newReader(zctx *resolver.Context, path string, level int) (*Reader, error) {
	path = iosource.NormalizePath(path)
	r, err := iosource.NewReader(filename(path, level))
	if err != nil {
		return nil, err
	}
	rs, ok := r.(io.ReadSeeker)
	if !ok {
		return nil, fmt.Errorf("underyling iosource.NewReader did not return a io.ReadSeeker")
	}
	seeker := zngio.NewSeekerWithSize(rs, zctx, FrameSize)
	return &Reader{
		Seeker: *seeker,
		reader: r,
	}, nil
}

func (r *Reader) Close() error {
	return r.reader.Close()
}
