package zqd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/brimsec/zq/pcap"
	"github.com/brimsec/zq/pkg/ctxio"
	"github.com/brimsec/zq/pkg/nano"
	"github.com/brimsec/zq/zqd/api"
	"github.com/brimsec/zq/zqd/ingest"
	"github.com/brimsec/zq/zqd/search"
	"github.com/brimsec/zq/zqd/space"
	"github.com/brimsec/zq/zqe"
	"github.com/gorilla/mux"
	"go.uber.org/zap"
)

func errorResponse(e error) (status int, ae *api.Error) {
	status = http.StatusInternalServerError
	ae = &api.Error{Type: "Error"}

	var ze *zqe.Error
	if !errors.As(e, &ze) {
		ae.Message = e.Error()
		return
	}

	switch ze.Kind {
	case zqe.Invalid:
		status = http.StatusBadRequest
	case zqe.NotFound:
		status = http.StatusNotFound
	case zqe.Exists:
		status = http.StatusBadRequest
	case zqe.Conflict:
		status = http.StatusConflict
	}

	ae.Kind = ze.Kind.String()
	ae.Message = ze.Message()
	return
}

func respond(c *Core, w http.ResponseWriter, r *http.Request, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		c.requestLogger(r).Warn("Error writing response", zap.Error(err))
	}
}

func respondError(c *Core, w http.ResponseWriter, r *http.Request, err error) {
	status, ae := errorResponse(err)
	if status >= 500 {
		c.requestLogger(r).Warn("error", zap.Int("status", status), zap.Error(err))
	}
	respond(c, w, r, status, ae)
}

func request(c *Core, w http.ResponseWriter, r *http.Request, apiobj interface{}) bool {
	if err := json.NewDecoder(r.Body).Decode(apiobj); err != nil {
		respondError(c, w, r, zqe.E(zqe.Invalid, err))
		return false
	}
	return true
}

func handleSearch(c *Core, w http.ResponseWriter, r *http.Request) {
	var req api.SearchRequest
	if !request(c, w, r, &req) {
		return
	}

	s, err := c.spaces.Get(req.Space)
	if err != nil {
		respondError(c, w, r, err)
		return
	}

	ctx, cancel, err := s.StartSpaceOp(r.Context())
	if err != nil {
		respondError(c, w, r, err)
		return
	}
	defer cancel()

	srch, err := search.NewSearch(ctx, s, req)
	if err != nil {
		// XXX This always returns bad request but should return status codes
		// that reflect the nature of the returned error.
		respondError(c, w, r, err)
		return
	}
	defer srch.Close()

	var out search.Output
	format := r.URL.Query().Get("format")
	switch format {
	case "zjson", "json":
		// XXX Should write appropriate ndjson content header.
		out = search.NewJSONOutput(w, search.DefaultMTU)
	case "zng":
		// XXX Should write appropriate zng content header.
		out = search.NewZngOutput(w)
	default:
		respondError(c, w, r, zqe.E(zqe.Invalid, "unsupported format: %s", format))
		return
	}
	// XXX This always returns bad request but should return status codes
	// that reflect the nature of the returned error.
	w.Header().Set("Content-Type", "application/ndjson")
	if err = srch.Run(out); err != nil {
		c.requestLogger(r).Warn("Error writing response", zap.Error(err))
	}
}

func handlePcapSearch(c *Core, w http.ResponseWriter, r *http.Request) {
	s := extractSpace(c, w, r)
	if s == nil {
		return
	}

	ctx, cancel, err := s.StartSpaceOp(r.Context())
	if err != nil {
		respondError(c, w, r, err)
		return
	}
	defer cancel()

	var req api.PcapSearch
	if err := req.FromQuery(r.URL.Query()); err != nil {
		respondError(c, w, r, zqe.E(zqe.Invalid, err))
		return
	}
	reader, err := s.PcapSearch(ctx, req)
	if err == pcap.ErrNoPcapsFound {
		respondError(c, w, r, zqe.E(zqe.NotFound, err))
		return
	}
	if err != nil {
		respondError(c, w, r, err)
		return
	}
	defer reader.Close()
	w.Header().Set("Content-Type", "application/vnd.tcpdump.pcap")
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%s.pcap", reader.ID()))
	_, err = ctxio.Copy(ctx, w, reader)
	if err != nil {
		c.requestLogger(r).Error("Error writing packet response", zap.Error(err))
	}
}

func handleSpaceList(c *Core, w http.ResponseWriter, r *http.Request) {
	spaces, err := c.spaces.List()
	if err != nil {
		respondError(c, w, r, err)
		return
	}
	respond(c, w, r, http.StatusOK, spaces)
}

func handleSpaceGet(c *Core, w http.ResponseWriter, r *http.Request) {
	s := extractSpace(c, w, r)
	if s == nil {
		return
	}

	_, cancel, err := s.StartSpaceOp(r.Context())
	if err != nil {
		respondError(c, w, r, err)
		return
	}
	defer cancel()

	info, err := s.Info()
	if err != nil {
		respondError(c, w, r, err)
		return
	}

	respond(c, w, r, http.StatusOK, info)
}

func handleSpacePost(c *Core, w http.ResponseWriter, r *http.Request) {
	var req api.SpacePostRequest
	if !request(c, w, r, &req) {
		return
	}

	sp, err := c.spaces.Create(req.Name, req.DataPath)
	if err != nil {
		respondError(c, w, r, err)
		return
	}
	info, err := sp.Info()
	if err != nil {
		respondError(c, w, r, err)
		return
	}

	respond(c, w, r, http.StatusOK, info)
}

func handleSpacePut(c *Core, w http.ResponseWriter, r *http.Request) {
	s := extractSpace(c, w, r)
	if s == nil {
		return
	}
	_, cancel, err := s.StartSpaceOp(r.Context())
	if err != nil {
		respondError(c, w, r, err)
		return
	}
	defer cancel()
	var req api.SpacePutRequest
	if !request(c, w, r, &req) {
		return
	}
	if err := s.Update(req); err != nil {
		respondError(c, w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func handleSpaceDelete(c *Core, w http.ResponseWriter, r *http.Request) {
	v := mux.Vars(r)
	id, ok := v["space"]
	if !ok {
		respondError(c, w, r, zqe.E(zqe.Invalid, "no space id in path"))
		return
	}

	err := c.spaces.Delete(api.SpaceID(id))
	if err != nil {
		respondError(c, w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func handlePcapPost(c *Core, w http.ResponseWriter, r *http.Request) {
	if !c.HasZeek() {
		respondError(c, w, r, zqe.E(zqe.Invalid, "packet post not supported: zeek not found"))
		return
	}
	logger := c.requestLogger(r)

	s := extractSpace(c, w, r)
	if s == nil {
		return
	}

	ctx, cancel, err := s.StartSpaceOp(r.Context())
	if err != nil {
		respondError(c, w, r, err)
		return
	}
	defer cancel()

	var req api.PcapPostRequest
	if !request(c, w, r, &req) {
		return
	}

	proc, err := ingest.Pcap(ctx, s, req.Path, c.ZeekLauncher)
	if err != nil {
		respondError(c, w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/ndjson")
	w.WriteHeader(http.StatusAccepted)
	pipe := api.NewJSONPipe(w)
	taskID := c.getTaskID()
	taskStart := api.TaskStart{Type: "TaskStart", TaskID: taskID}
	if err = pipe.Send(taskStart); err != nil {
		logger.Warn("Error sending payload", zap.Error(err))
		return
	}
	ticker := time.NewTicker(time.Second * 2)
	defer ticker.Stop()
	for {
		var done bool
		select {
		case <-proc.Done():
			done = true
		case <-proc.Snap():
		case <-ticker.C:
		}

		var span *nano.Span
		if span, err = s.Span(); err != nil {
			break
		}
		status := api.PcapPostStatus{
			Type:          "PcapPostStatus",
			StartTime:     proc.StartTime,
			UpdateTime:    nano.Now(),
			PcapSize:      proc.PcapSize,
			PcapReadSize:  proc.PcapReadSize(),
			SnapshotCount: proc.SnapshotCount(),
			Span:          span,
		}
		if err := pipe.Send(status); err != nil {
			logger.Warn("Error sending payload", zap.Error(err))
			return
		}
		if done {
			break
		}
	}
	taskEnd := api.TaskEnd{Type: "TaskEnd", TaskID: taskID}
	if err := proc.Err(); err != nil {
		var ok bool
		taskEnd.Error, ok = err.(*api.Error)
		if !ok {
			taskEnd.Error = &api.Error{Type: "Error", Message: err.Error()}
		}
	}
	if err = pipe.SendFinal(taskEnd); err != nil {
		logger.Warn("Error sending payload", zap.Error(err))
		return
	}
}

func handleLogPost(c *Core, w http.ResponseWriter, r *http.Request) {
	s := extractSpace(c, w, r)
	if s == nil {
		return
	}

	ctx, cancel, err := s.StartSpaceOp(r.Context())
	if err != nil {
		respondError(c, w, r, err)
		return
	}
	defer cancel()

	var req api.LogPostRequest
	if !request(c, w, r, &req) {
		return
	}
	if len(req.Paths) == 0 {
		respondError(c, w, r, zqe.E(zqe.Invalid, "empty paths"))
		return
	}
	transaction, err := ingest.NewLogTransaction(ctx, s.Storage, req)
	if err != nil {
		respondError(c, w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/ndjson")
	w.WriteHeader(http.StatusAccepted)

	pipe := api.NewJSONPipe(w)
	pipe.SendStart(0)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case warning := <-transaction.Warning():
			pipe.Send(api.LogPostWarning{
				Type:    "LogPostWarning",
				Warning: warning,
			})
		case <-transaction.Done():
			// send final status
			status := transaction.Status()
			pipe.Send(status)
			pipe.SendEnd(0, transaction.Error())
			return
		case <-ticker.C:
			status := transaction.Status()
			pipe.Send(status)
		}
	}
}

func extractSpace(c *Core, w http.ResponseWriter, r *http.Request) *space.Space {
	v := mux.Vars(r)
	id, ok := v["space"]
	if !ok {
		respondError(c, w, r, zqe.E(zqe.Invalid, "no space id in path"))
		return nil
	}
	s, err := c.spaces.Get(api.SpaceID(id))
	if err != nil {
		respondError(c, w, r, err)
		return nil
	}
	return s
}
