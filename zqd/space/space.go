package space

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/brimsec/zq/pkg/nano"
	"github.com/brimsec/zq/zqd/api"
	"github.com/brimsec/zq/zqd/storage"
	"github.com/brimsec/zq/zqd/storage/archivestore"
	"github.com/brimsec/zq/zqd/storage/filestore"
	"github.com/brimsec/zq/zqe"
	"github.com/segmentio/ksuid"
)

const (
	configFile = "config.json"
)

var (
	ErrPcapOpsNotSupported = zqe.E(zqe.Invalid, "space does not support pcap operations")
	ErrSpaceExists         = zqe.E(zqe.Exists, "space exists")
	ErrSpaceNotExist       = zqe.E(zqe.NotFound, "space does not exist")
)

type Space interface {
	ID() api.SpaceID
	Name() string
	Storage() storage.Storage
	Info(context.Context) (api.SpaceInfo, error)

	// StartOp is called to register an operation is in progress; the
	// returned cancel function must be called when the operation is done.
	StartOp(context.Context) (context.Context, context.CancelFunc, error)

	// Delete cancels any outstanding operations, then removes the space's path
	// and data dir (should the data dir be different than the space's path).
	// Intended to be called from Manager.Delete().
	delete() error
	update(api.SpacePutRequest) error
}

func newSpaceID() api.SpaceID {
	id := ksuid.New()
	return api.SpaceID(fmt.Sprintf("sp_%s", id.String()))
}

type guard struct {
	// state about operations in progress
	mu            sync.Mutex
	deletePending bool

	wg sync.WaitGroup
	// closed to signal non-delete ops should terminate
	cancelChan chan struct{}
}

func newGuard() *guard {
	return &guard{
		cancelChan: make(chan struct{}),
	}
}

func (g *guard) acquire(ctx context.Context) (context.Context, context.CancelFunc, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.deletePending {
		return ctx, func() {}, zqe.E(zqe.Conflict, "space is pending deletion")
	}

	g.wg.Add(1)

	ctx, cancel := context.WithCancel(ctx)
	go func() {
		select {
		case <-ctx.Done():
		case <-g.cancelChan:
			cancel()
		}
	}()

	done := func() {
		g.wg.Done()
		cancel()
	}

	return ctx, done, nil
}

func (g *guard) acquireForDelete() error {
	g.mu.Lock()

	if g.deletePending {
		g.mu.Unlock()
		return zqe.E(zqe.Conflict, "space is pending deletion")
	}
	g.deletePending = true
	g.mu.Unlock()

	close(g.cancelChan)
	g.wg.Wait()
	return nil
}

func loadSpaces(path string, conf config) ([]Space, error) {
	datapath := conf.DataPath
	if datapath == "." {
		datapath = path
	}

	id := api.SpaceID(filepath.Base(path))
	switch conf.Storage.Kind {
	case storage.FileStore:
		store, err := filestore.Load(datapath)
		if err != nil {
			return nil, err
		}
		s := &fileSpace{
			spaceBase: spaceBase{id, store, newGuard()},
			path:      path,
			conf:      conf,
		}
		return []Space{s}, nil

	case storage.ArchiveStore:
		store, err := archivestore.Load(datapath, conf.Storage.Archive)
		if err != nil {
			return nil, err
		}
		parent := &archiveSpace{
			spaceBase: spaceBase{id, store, newGuard()},
			path:      path,
			conf:      conf,
		}
		ret := []Space{parent}
		for _, subcfg := range conf.Subspaces {
			substore, err := archivestore.Load(datapath, &storage.ArchiveConfig{
				OpenOptions: &subcfg.OpenOptions,
			})
			if err != nil {
				return nil, err
			}
			sub := &archiveSubspace{
				spaceBase: spaceBase{subcfg.ID, substore, newGuard()},
				parent:    parent,
			}
			ret = append(ret, sub)
		}
		return ret, nil

	default:
		return nil, zqe.E(zqe.Invalid, "loadSpace: unknown storage kind: %s", conf.Storage.Kind)
	}
}

// spaceBase contains the basic fields common to different space types.
type spaceBase struct {
	id    api.SpaceID
	store storage.Storage
	sg    *guard
}

func (s *spaceBase) ID() api.SpaceID {
	return s.id
}

func (s *spaceBase) Storage() storage.Storage {
	return s.store
}

func (s *spaceBase) Info(ctx context.Context) (api.SpaceInfo, error) {
	sum, err := s.store.Summary(ctx)
	if err != nil {
		return api.SpaceInfo{}, err
	}
	var span *nano.Span
	if sum.Span.Dur > 0 {
		span = &sum.Span
	}
	spaceInfo := api.SpaceInfo{
		ID:          s.id,
		StorageKind: sum.Kind,
		Size:        sum.DataBytes,
		Span:        span,
	}
	return spaceInfo, nil
}

// StartOp registers that an operation on this space is in progress.
// If the space is pending deletion, an error is returned.
// Otherwise, this returns a new context, and a done function that must
// be called when the operation completes.
func (s *spaceBase) StartOp(ctx context.Context) (context.Context, context.CancelFunc, error) {
	return s.sg.acquire(ctx)
}
