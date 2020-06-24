package archive

import (
	"encoding/json"
	"errors"
	"sort"

	"github.com/brimsec/zq/pkg/iosrc"
	"github.com/brimsec/zq/pkg/nano"
	"github.com/brimsec/zq/zbuf"
	"github.com/brimsec/zq/zqe"
)

const metadataFilename = "zar.json"

type Metadata struct {
	Version           int            `json:"version"`
	LogSizeThreshold  int64          `json:"log_size_threshold"`
	DataSortDirection zbuf.Direction `json:"data_sort_direction"`
	Spans             []SpanInfo     `json:"spans"`
}

// A LogID identifies a single zng file within an archive. It is created
// by doing a path join (with forward slashes, regardless of platform)
// of the relative location of the file under the archive's root directory.
type LogID string

// Path returns the local filesystem path for the log file, using the
// platforms file separator.
func (l LogID) Path(ark *Archive) iosrc.URI {
	return ark.Root.AppendPath(string(l))
}

type SpanInfo struct {
	Span  nano.Span `json:"span"`
	LogID LogID     `json:"log_id"`
}

func (c *Metadata) Write(src iosrc.Source, uri iosrc.URI) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if atomicw, ok := src.(iosrc.AtomicWriter); ok {
		return atomicw.AtomicWrite(uri, b)
	}
	w, err := src.NewWriter(uri)
	if err != nil {
		return err
	}
	if _, err := w.Write(b); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

func ConfigRead(src iosrc.Source, uri iosrc.URI) (*Metadata, error) {
	r, err := src.NewReader(uri)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	var m Metadata
	return &m, json.NewDecoder(r).Decode(&m)
}

const (
	DefaultLogSizeThreshold  = 500 * 1024 * 1024
	DefaultDataSortDirection = zbuf.DirTimeReverse
)

type CreateOptions struct {
	LogSizeThreshold *int64
}

func (c *CreateOptions) toMetadata() *Metadata {
	m := &Metadata{
		Version:           0,
		LogSizeThreshold:  DefaultLogSizeThreshold,
		DataSortDirection: DefaultDataSortDirection,
	}

	if c.LogSizeThreshold != nil {
		m.LogSizeThreshold = *c.LogSizeThreshold
	}

	return m
}

type Archive struct {
	Meta *Metadata
	Root iosrc.URI

	// Spans contains either all spans from metadata, or a subset
	// due to opening the archive with a filter list.
	Spans []SpanInfo

	Source iosrc.Source
}

func (ark *Archive) AppendSpans(spans []SpanInfo) error {
	ark.Meta.Spans = append(ark.Meta.Spans, spans...)

	sort.Slice(ark.Meta.Spans, func(i, j int) bool {
		if ark.Meta.DataSortDirection == zbuf.DirTimeForward {
			return ark.Meta.Spans[i].Span.Ts < ark.Meta.Spans[j].Span.Ts
		}
		return ark.Meta.Spans[j].Span.Ts < ark.Meta.Spans[i].Span.Ts
	})

	return ark.Meta.Write(ark.Source, ark.Root.AppendPath(metadataFilename))
}

type OpenOptions struct {
	LogFilter []string
}

func OpenArchive(root string, oo *OpenOptions) (*Archive, error) {
	if root == "" {
		return nil, errors.New("no archive directory specified")
	}
	uri, err := iosrc.ParseURI(root)
	if err != nil {
		return nil, err
	}
	src, err := iosrc.DefaultRegistry.Source(uri)
	if err != nil {
		return nil, err
	}
	c, err := ConfigRead(src, uri.AppendPath(metadataFilename))
	if err != nil {
		return nil, err
	}

	var spans []SpanInfo
	if oo != nil && len(oo.LogFilter) != 0 {
		lmap := make(map[LogID]struct{})
		for _, l := range oo.LogFilter {
			lmap[LogID(l)] = struct{}{}
		}
		for _, s := range c.Spans {
			if _, ok := lmap[s.LogID]; ok {
				spans = append(spans, s)
			}
		}
		if len(spans) == 0 {
			return nil, zqe.E(zqe.Invalid, "OpenArchive: no spans left after filter")
		}
	} else {
		spans = c.Spans
	}

	return &Archive{
		Meta:   c,
		Root:   uri,
		Spans:  spans,
		Source: src,
	}, nil
}

func CreateOrOpenArchive(root string, co *CreateOptions, oo *OpenOptions) (*Archive, error) {
	if root == "" {
		return nil, errors.New("no archive directory specified")
	}
	uri, err := iosrc.ParseURI(root)
	if err != nil {
		return nil, err
	}
	src, err := iosrc.DefaultRegistry.Source(uri)
	if err != nil {
		return nil, err
	}
	cfguri := uri.AppendPath(metadataFilename)
	ok, err := src.Exists(cfguri)
	if err != nil {
		return nil, err
	}
	if !ok {
		if mkdir, ok := src.(iosrc.DirMaker); ok {
			if err := mkdir.MkdirAll(cfguri, 0700); err != nil {
				return nil, err
			}
		}
		if err = co.toMetadata().Write(src, cfguri); err != nil {
			return nil, err
		}
	}
	return OpenArchive(root, oo)
}
