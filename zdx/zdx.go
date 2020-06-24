// Package zdx provides an API for creating, merging, indexing, and querying zng
// index files, a la SST in LSM trees, where a zdx "bundle" comprises a sequence of
// key,value zng records and the records are sorted by key.
//
// A table on disk consists of the base table with zero or more b-tree files.
// These files are all named with the same path prefix, e.g., "zdx.zng", where the
// base table is zdx.zng and the b-tree index files, if any, are zdx.1.zng,
// zdx.2.zng, and so forth.  The index files can always be reconstructed from
// the base table.
//
// Typically when building such a table, a client starts out with the table
// in memory.  Then it is written to storage using a Writer.  Tables can be
// combined with a Combiner and are merged in an efficient LSM like fashion.
//
// The zdx base file and index files are all organized as a sequence of
// zng streams.
//
// Each b-tree index file contains a key,value pair for each stream in the file
// below in the hiearchy where the key is the first key found in that stream and
// the value is the offset or the stream in the file below.
//
// zdx.Reader implements zbuf.Reader and zdx.Writer implements zbuf.Writer and
// zbuf.WriteFlusher so generic zng functionality applies, e.g., a Reader can be
// copied to a Writer using zbuf.Copy().
package zdx

import (
	"errors"
	"fmt"
	"os"

	"github.com/brimsec/zq/pkg/iosource"
)

var (
	ErrCorruptFile = errors.New("corrupt zdx file")
)

func filename(path string, level int) string {
	if level == 0 {
		return fmt.Sprintf("%s.zng", path)
	}
	return fmt.Sprintf("%s.%d.zng", path, level)
}

func Remove(src iosource.Source, path string) error {
	level := 0
	for {
		err := src.Remove(filename(path, level))
		if err != nil {
			// TODO
			if os.IsNotExist(err) {
				err = nil
			}
			return err
		}
		level++
	}
}

// func Rename(src iosource.Source, oldpath, newpath string) error {
// level := 0
// for {
// err := src.Rename(filename(oldpath, level), filename(newpath, level))
// if err != nil {
// // XXX
// if os.IsNotExist(err) {
// err = nil
// }
// return err
// }
// level++
// }
// }
