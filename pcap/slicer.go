package pcap

import (
	"os"

	"github.com/brimsec/zq/pkg/nano"
	"github.com/brimsec/zq/pkg/ranger"
	"github.com/brimsec/zq/pkg/slicer"
)

func NewSlicer(file *os.File, index Index, span nano.Span) (*slicer.Reader, error) {
	slices, err := GenerateSlices(index, span)
	if err != nil {
		return nil, err
	}
	return slicer.NewReader(file, slices)
}

// GenerateSlices takes an index and time span and generates a list of
// slices that should be read to enumerate the relevant chunks of an
// underlying pcap file.  Extra packets may appear in the resulting stream
// but all packets that fall within the time range will be produced, i.e.,
// another layering of time filtering should be applied to resulting packets.
func GenerateSlices(index Index, span nano.Span) ([]slicer.Slice, error) {
	var slices []slicer.Slice
	for _, section := range index {
		pslice, err := FindPacketSlice(section.Index, span)
		if err == ErrNoPcapsFound {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, slice := range section.Blocks {
			slices = append(slices, slice)
		}
		slices = append(slices, pslice)
	}
	return slices, nil
}

func FindPacketSlice(e ranger.Envelope, span nano.Span) (slicer.Slice, error) {
	if len(e) == 0 {
		return slicer.Slice{}, ErrNoPcapsFound
	}
	d := e.FindSmallestDomain(ranger.Range{uint64(span.Ts), uint64(span.End())})
	gap := d.X1 - d.X0
	if gap == 0 {
		return slicer.Slice{}, ErrNoPcapsFound
	}
	return slicer.Slice{d.X0, d.X1 - d.X0}, nil
}
