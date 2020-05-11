package zqd_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/brimsec/zq/pkg/nano"
	"github.com/brimsec/zq/pkg/test"
	"github.com/brimsec/zq/zbuf"
	"github.com/brimsec/zq/zio/ndjsonio"
	"github.com/brimsec/zq/zio/tzngio"
	"github.com/brimsec/zq/zqd"
	"github.com/brimsec/zq/zqd/api"
	"github.com/brimsec/zq/zqd/zeek"
	"github.com/brimsec/zq/zql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

func TestSearch(t *testing.T) {
	src := `
#0:record[_path:string,ts:time,uid:bstring]
0:[conn;1521911723.205187;CBrzd94qfowOqJwCHa;]
0:[conn;1521911721.255387;C8Tful1TvM3Zf5x8fl;]
`
	_, client, done := newCore(t)
	defer done()
	sp, err := client.SpacePost(context.Background(), api.SpacePostRequest{Name: "test"})
	require.NoError(t, err)
	_ = postSpaceLogs(t, client, sp.ID, nil, src)
	res := searchTzng(t, client, sp.ID, "*")
	require.Equal(t, test.Trim(src), res)
}

func TestSearchStats(t *testing.T) {
	src := `
#0:record[_path:string,ts:time]
0:[a;1;]
0:[b;1;]
`
	_, client, done := newCore(t)
	defer done()
	sp, err := client.SpacePost(context.Background(), api.SpacePostRequest{Name: "test"})
	require.NoError(t, err)
	_ = postSpaceLogs(t, client, sp.ID, nil, src)
	_, msgs := search(t, client, sp.ID, "_path != b")
	var stats *api.SearchStats
	for i := len(msgs) - 1; i >= 0; i-- {
		if s, ok := msgs[i].(*api.SearchStats); ok {
			stats = s
			break
		}
	}
	require.NotNil(t, stats)
	assert.Equal(t, stats.Type, "SearchStats")
	assert.Equal(t, stats.ScannerStats, api.ScannerStats{
		BytesRead:      14,
		BytesMatched:   7,
		RecordsRead:    2,
		RecordsMatched: 1,
	})
}

func TestGroupByReverse(t *testing.T) {
	src := `
#0:record[_path:string,ts:time,uid:bstring]
0:[conn;1;CBrzd94qfowOqJwCHa;]
0:[conn;1;C8Tful1TvM3Zf5x8fl;]
0:[conn;2;C8Tful1TvM3Zf5x8fl;]
`
	counts := `
#0:record[ts:time,count:uint64]
0:[2;1;]
0:[1;2;]
`
	_, client, done := newCore(t)
	defer done()
	sp, err := client.SpacePost(context.Background(), api.SpacePostRequest{Name: "test"})
	require.NoError(t, err)
	_ = postSpaceLogs(t, client, sp.ID, nil, src)
	res := searchTzng(t, client, sp.ID, "every 1s count()")
	require.Equal(t, test.Trim(counts), res)
}

func TestSearchEmptySpace(t *testing.T) {
	ctx := context.Background()
	_, client, done := newCore(t)
	defer done()
	sp, err := client.SpacePost(ctx, api.SpacePostRequest{Name: "test"})
	require.NoError(t, err)
	res := searchTzng(t, client, sp.ID, "*")
	require.Equal(t, "", res)
}

func TestSearchError(t *testing.T) {
	src := `
#0:record[_path:string,ts:time,uid:bstring]
0:[conn;1521911723.205187;CBrzd94qfowOqJwCHa;]
0:[conn;1521911721.255387;C8Tful1TvM3Zf5x8fl;]
`
	_, client, done := newCore(t)
	defer done()
	sp, err := client.SpacePost(context.Background(), api.SpacePostRequest{Name: "test"})
	require.NoError(t, err)
	_ = postSpaceLogs(t, client, sp.ID, nil, src)

	parsed, err := zql.ParseProc("*")
	require.NoError(t, err)
	proc, err := json.Marshal(parsed)
	require.NoError(t, err)
	t.Run("InvalidDir", func(t *testing.T) {
		req := api.SearchRequest{
			Space: sp.ID,
			Proc:  proc,
			Span:  nano.MaxSpan,
			Dir:   2,
		}
		_, err = client.Search(context.Background(), req)
		require.Error(t, err)
		errResp := err.(*api.ErrorResponse)
		assert.Equal(t, http.StatusBadRequest, errResp.StatusCode())
		assert.IsType(t, &api.Error{}, errResp.Err)
	})
	t.Run("ForwardSearchUnsupported", func(t *testing.T) {
		req := api.SearchRequest{
			Space: sp.ID,
			Proc:  proc,
			Span:  nano.MaxSpan,
			Dir:   1,
		}
		_, err = client.Search(context.Background(), req)
		require.Error(t, err)
		errResp := err.(*api.ErrorResponse)
		assert.Equal(t, http.StatusBadRequest, errResp.StatusCode())
		assert.IsType(t, &api.Error{}, errResp.Err)
	})
}

func TestSpaceList(t *testing.T) {
	names := []string{"sp1", "sp2", "sp3", "sp4"}
	var expected []api.SpaceInfo

	ctx := context.Background()
	c, client, done := newCore(t)
	{
		defer done()

		for _, n := range names {
			sp, err := client.SpacePost(ctx, api.SpacePostRequest{Name: n})
			require.NoError(t, err)
			expected = append(expected, api.SpaceInfo{
				ID:       sp.ID,
				Name:     n,
				DataPath: filepath.Join(c.Root, string(sp.ID)),
			})
		}

		list, err := client.SpaceList(ctx)
		require.NoError(t, err)
		sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
		require.Equal(t, expected, list)
	}

	// Delete dir from one space, then simulate a restart by
	// creating a new Core pointing to the same root.
	require.NoError(t, os.RemoveAll(filepath.Join(c.Root, string(expected[2].ID))))
	expected = append(expected[:2], expected[3:]...)

	c, client, done = newCoreAtDir(t, c.Root)
	defer done()

	list, err := client.SpaceList(ctx)
	require.NoError(t, err)
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	require.Equal(t, expected, list)
}

func TestSpaceInfo(t *testing.T) {
	src := `
#0:record[_path:string,ts:time,uid:bstring]
0:[conn;1;CBrzd94qfowOqJwCHa;]
0:[conn;2;C8Tful1TvM3Zf5x8fl;]`
	ctx := context.Background()
	_, client, done := newCore(t)
	defer done()
	sp, err := client.SpacePost(ctx, api.SpacePostRequest{Name: "test"})
	require.NoError(t, err)
	_ = postSpaceLogs(t, client, sp.ID, nil, src)
	span := nano.Span{Ts: 1e9, Dur: 1e9 + 1}
	expected := &api.SpaceInfo{
		ID:          sp.ID,
		Name:        sp.Name,
		DataPath:    sp.DataPath,
		Span:        &span,
		Size:        81,
		PcapSupport: false,
	}
	info, err := client.SpaceInfo(ctx, sp.ID)
	require.NoError(t, err)
	require.Equal(t, expected, info)
}

func TestSpaceInfoNoData(t *testing.T) {
	ctx := context.Background()
	_, client, done := newCore(t)
	defer done()
	sp, err := client.SpacePost(ctx, api.SpacePostRequest{Name: "test"})
	require.NoError(t, err)
	info, err := client.SpaceInfo(ctx, sp.ID)
	require.NoError(t, err)
	expected := &api.SpaceInfo{
		ID:          sp.ID,
		Name:        sp.Name,
		DataPath:    sp.DataPath,
		Size:        0,
		PcapSupport: false,
	}
	require.Equal(t, expected, info)
}

func TestSpacePostNameOnly(t *testing.T) {
	ctx := context.Background()
	c, client, done := newCore(t)
	defer done()
	sp, err := client.SpacePost(ctx, api.SpacePostRequest{Name: "test"})
	require.NoError(t, err)
	assert.Equal(t, "test", sp.Name)
	assert.Equal(t, filepath.Join(c.Root, string(sp.ID)), sp.DataPath)
	assert.Regexp(t, "^sp", sp.ID)
}

func TestSpacePostDataPath(t *testing.T) {
	ctx := context.Background()
	tmp := createTempDir(t)
	defer os.RemoveAll(tmp)
	datapath := filepath.Join(tmp, "mypcap.brim")
	_, client, done := newCoreAtDir(t, filepath.Join(tmp, "spaces"))
	defer done()
	sp, err := client.SpacePost(ctx, api.SpacePostRequest{DataPath: datapath})
	require.NoError(t, err)
	assert.Equal(t, "mypcap.brim", sp.Name)
	assert.Equal(t, datapath, sp.DataPath)
}

func TestSpacePut(t *testing.T) {
	ctx := context.Background()
	_, client, done := newCore(t)
	defer done()
	sp, err := client.SpacePost(ctx, api.SpacePostRequest{Name: "test"})
	require.NoError(t, err)
	err = client.SpacePut(ctx, sp.ID, api.SpacePutRequest{Name: "new_name"})
	require.NoError(t, err)
	info, err := client.SpaceInfo(ctx, sp.ID)
	require.NoError(t, err)
	assert.Equal(t, "new_name", info.Name)
}

func TestSpaceDelete(t *testing.T) {
	ctx := context.Background()
	_, client, done := newCore(t)
	defer done()
	sp, err := client.SpacePost(ctx, api.SpacePostRequest{Name: "test"})
	require.NoError(t, err)
	err = client.SpaceDelete(ctx, sp.ID)
	require.NoError(t, err)
	list, err := client.SpaceList(ctx)
	require.NoError(t, err)
	require.Equal(t, []api.SpaceInfo{}, list)
}

func TestSpaceDeleteDataDir(t *testing.T) {
	ctx := context.Background()
	tmp := createTempDir(t)
	defer os.RemoveAll(tmp)
	_, client, done := newCoreAtDir(t, filepath.Join(tmp, "spaces"))
	defer done()
	datadir := filepath.Join(tmp, "mypcap.brim")
	sp, err := client.SpacePost(ctx, api.SpacePostRequest{Name: "test"})
	require.NoError(t, err)
	err = client.SpaceDelete(ctx, sp.ID)
	require.NoError(t, err)
	list, err := client.SpaceList(ctx)
	require.NoError(t, err)
	require.Equal(t, []api.SpaceInfo{}, list)
	// ensure data dir has also been deleted
	_, err = os.Stat(datadir)
	require.Error(t, err)
	require.Truef(t, os.IsNotExist(err), "expected error to be os.IsNotExist, got %v", err)
}

func TestNoEndSlashSupport(t *testing.T) {
	_, client, done := newCore(t)
	defer done()
	_, err := client.Do(context.Background(), "GET", "/space/", nil)
	require.Error(t, err)
	require.Equal(t, 404, err.(*api.ErrorResponse).StatusCode())
}

func TestRequestID(t *testing.T) {
	ctx := context.Background()
	t.Run("GeneratesUniqueID", func(t *testing.T) {
		_, client, done := newCore(t)
		defer done()
		res1, err := client.Do(ctx, "GET", "/space", nil)
		require.NoError(t, err)
		res2, err := client.Do(ctx, "GET", "/space", nil)
		require.NoError(t, err)
		assert.Equal(t, "1", res1.Header().Get("X-Request-ID"))
		assert.Equal(t, "2", res2.Header().Get("X-Request-ID"))
	})
	t.Run("PropagatesID", func(t *testing.T) {
		_, client, done := newCore(t)
		defer done()
		requestID := "random-request-ID"
		req := client.Request(context.Background())
		req.SetHeader("X-Request-ID", requestID)
		res, err := req.Execute("GET", "/space")
		require.NoError(t, err)
		require.Equal(t, requestID, res.Header().Get("X-Request-ID"))
	})
}

func TestPostZngLogs(t *testing.T) {
	src1 := []string{
		"#0:record[_path:string,ts:time,uid:bstring]",
		"0:[conn;1;CBrzd94qfowOqJwCHa;]",
	}
	src2 := []string{
		"#0:record[_path:string,ts:time,uid:bstring]",
		"0:[conn;2;CBrzd94qfowOqJwCHa;]",
	}
	_, client, done := newCore(t)
	defer done()
	sp, err := client.SpacePost(context.Background(), api.SpacePostRequest{Name: "test"})
	require.NoError(t, err)

	payloads := postSpaceLogs(t, client, sp.ID, nil, strings.Join(src1, "\n"), strings.Join(src2, "\n"))
	status := payloads[len(payloads)-2].(*api.LogPostStatus)
	span := &nano.Span{Ts: 1e9, Dur: 1e9 + 1}
	require.Equal(t, &api.LogPostStatus{
		Type:         "LogPostStatus",
		LogTotalSize: 148,
		LogReadSize:  148,
	}, status)

	taskend := payloads[len(payloads)-1].(*api.TaskEnd)
	assert.Equal(t, taskend.Type, "TaskEnd")
	assert.Nil(t, taskend.Error)

	res := searchTzng(t, client, sp.ID, "*")
	require.Equal(t, strings.Join(append(src2, src1[1]), "\n"), strings.TrimSpace(res))

	info, err := client.SpaceInfo(context.Background(), sp.ID)
	require.NoError(t, err)
	require.Equal(t, &api.SpaceInfo{
		ID:          sp.ID,
		Name:        sp.Name,
		DataPath:    sp.DataPath,
		Span:        span,
		Size:        81,
		PcapSupport: false,
	}, info)
}

func TestPostZngLogWarning(t *testing.T) {
	src1 := []string{
		"undetectableformat",
	}
	src2 := []string{
		"#0:record[_path:string,ts:time,uid:bstring]",
		"0:[conn;1;CBrzd94qfowOqJwCHa;]",
		"detectablebutbadline",
	}
	_, client, done := newCore(t)
	defer done()
	sp, err := client.SpacePost(context.Background(), api.SpacePostRequest{Name: "test"})
	require.NoError(t, err)

	payloads := postSpaceLogs(t, client, sp.ID, nil, strings.Join(src1, "\n"), strings.Join(src2, "\n"))
	warn1 := payloads[1].(*api.LogPostWarning)
	warn2 := payloads[2].(*api.LogPostWarning)
	assert.Regexp(t, ": format detection error.*", warn1.Warning)
	assert.Regexp(t, ": line 3: bad format$", warn2.Warning)

	status := payloads[len(payloads)-2].(*api.LogPostStatus)
	expected := &api.LogPostStatus{
		Type:         "LogPostStatus",
		LogTotalSize: 95,
		LogReadSize:  95,
	}
	require.Equal(t, expected, status)

	taskend := payloads[len(payloads)-1].(*api.TaskEnd)
	assert.Equal(t, taskend.Type, "TaskEnd")
	assert.Nil(t, taskend.Error)
}

func TestPostNDJSONLogs(t *testing.T) {
	const src = `{"ts":"1000","uid":"CXY9a54W2dLZwzPXf1","_path":"http"}
{"ts":"2000","uid":"CXY9a54W2dLZwzPXf1","_path":"http"}`
	const expected = "#0:record[_path:string,ts:time,uid:bstring]\n0:[http;2;CXY9a54W2dLZwzPXf1;]\n0:[http;1;CXY9a54W2dLZwzPXf1;]"
	tc := ndjsonio.TypeConfig{
		Descriptors: map[string][]interface{}{
			"http_log": []interface{}{
				map[string]interface{}{
					"name": "_path",
					"type": "string",
				},
				map[string]interface{}{
					"name": "ts",
					"type": "time",
				},
				map[string]interface{}{
					"name": "uid",
					"type": "bstring",
				},
			},
		},
		Rules: []ndjsonio.Rule{
			ndjsonio.Rule{"_path", "http", "http_log"},
		},
	}

	test := func(input string) {
		_, client, done := newCore(t)
		defer done()

		sp, err := client.SpacePost(context.Background(), api.SpacePostRequest{Name: "test"})
		require.NoError(t, err)

		payloads := postSpaceLogs(t, client, sp.ID, &tc, input)
		last := payloads[len(payloads)-1].(*api.TaskEnd)
		assert.Equal(t, last.Type, "TaskEnd")
		assert.Nil(t, last.Error)

		res := searchTzng(t, client, sp.ID, "*")
		require.Equal(t, expected, strings.TrimSpace(res))

		span := nano.Span{Ts: 1e9, Dur: 1e9 + 1}
		info, err := client.SpaceInfo(context.Background(), sp.ID)
		require.NoError(t, err)
		require.Equal(t, &api.SpaceInfo{
			ID:          sp.ID,
			Name:        sp.Name,
			DataPath:    sp.DataPath,
			Span:        &span,
			Size:        81,
			PcapSupport: false,
		}, info)
	}
	t.Run("plain", func(t *testing.T) {
		test(src)
	})
	t.Run("gzipped", func(t *testing.T) {
		var b strings.Builder
		w := gzip.NewWriter(&b)
		_, err := w.Write([]byte(src))
		require.NoError(t, err)
		require.NoError(t, w.Close())
		test(b.String())
	})
}

func TestPostNDJSONLogWarning(t *testing.T) {
	const src1 = `{"ts":"1000","_path":"nosuchpath"}
{"ts":"2000","_path":"http"}`
	const src2 = `{"ts":"1000","_path":"http"}
{"ts":"1000","_path":"http","extra":"foo"}`
	tc := ndjsonio.TypeConfig{
		Descriptors: map[string][]interface{}{
			"http_log": []interface{}{
				map[string]interface{}{
					"name": "_path",
					"type": "string",
				},
				map[string]interface{}{
					"name": "ts",
					"type": "time",
				},
			},
		},
		Rules: []ndjsonio.Rule{
			ndjsonio.Rule{"_path", "http", "http_log"},
		},
	}
	_, client, done := newCore(t)
	defer done()
	sp, err := client.SpacePost(context.Background(), api.SpacePostRequest{Name: "test"})
	require.NoError(t, err)

	payloads := postSpaceLogs(t, client, sp.ID, &tc, src1, src2)
	warn1 := payloads[1].(*api.LogPostWarning)
	warn2 := payloads[2].(*api.LogPostWarning)
	assert.Regexp(t, ": line 1: descriptor not found$", warn1.Warning)
	assert.Regexp(t, ": line 2: incomplete descriptor", warn2.Warning)

	status := payloads[len(payloads)-2].(*api.LogPostStatus)
	expected := &api.LogPostStatus{
		Type:         "LogPostStatus",
		LogTotalSize: 134,
		LogReadSize:  134,
	}
	require.Equal(t, expected, status)

	taskend := payloads[len(payloads)-1].(*api.TaskEnd)
	assert.Equal(t, taskend.Type, "TaskEnd")
	assert.Nil(t, taskend.Error)
}

// func TestPostLogStopErr(t *testing.T) {
// src := `
// #0:record[_path:string,ts:time,uid:bstring
// 0:[conn;1;CBrzd94qfowOqJwCHa;]`
// _, client, done := newCore(t)
// defer done()

// sp, err := client.SpacePost(context.Background(), api.SpacePostRequest{Name: "test"})
// require.NoError(t, err)

// payloads := postSpaceLogs(t, client, sp.ID, nil, src)
// last := payloads[len(payloads)-1].(*api.TaskEnd)
// assert.Equal(t, last.Type, "TaskEnd")
// require.NotNil(t, last.Error)
// assert.Regexp(t, ": format detection error.*", last.Error.Message)
// }

func TestDeleteDuringPcapPost(t *testing.T) {
	c, client, done := newCore(t)
	defer done()

	pcapfile := "./testdata/valid.pcap"
	sp, err := client.SpacePost(context.Background(), api.SpacePostRequest{Name: "deleteDuringPacketPost"})
	require.NoError(t, err)

	waitFn := func(tzp *testZeekProcess) error {
		<-tzp.ctx.Done()
		return tzp.ctx.Err()
	}

	c.ZeekLauncher = testZeekLauncher(nil, waitFn)

	var wg sync.WaitGroup
	pcapPostErr := make(chan error)

	wg.Add(1)
	doPost := func() error {
		stream, err := client.PcapPost(context.Background(), sp.ID, api.PcapPostRequest{pcapfile})
		if err != nil {
			return err
		}
		wg.Done()

		var taskEnd *api.TaskEnd
		for {
			i, err := stream.Next()
			if err != nil {
				return err
			}
			if i == nil {
				break
			}
			if te, ok := i.(*api.TaskEnd); ok {
				taskEnd = te
			}
		}
		if taskEnd == nil {
			return errors.New("no TaskEnd payload")
		}
		return *taskEnd.Error
	}
	go func() {
		pcapPostErr <- doPost()
	}()

	wg.Wait()
	err = client.SpaceDelete(context.Background(), sp.ID)
	require.NoError(t, err)

	require.Error(t, <-pcapPostErr, "context canceled")
}

// search runs the provided zql program as a search on the provided
// space, returning the tzng results along with a slice of all control
// messages that were received.
func search(t *testing.T, client *api.Connection, space api.SpaceID, prog string) (string, []interface{}) {
	parsed, err := zql.ParseProc(prog)
	require.NoError(t, err)
	proc, err := json.Marshal(parsed)
	require.NoError(t, err)
	req := api.SearchRequest{
		Space: space,
		Proc:  proc,
		Span:  nano.MaxSpan,
		Dir:   -1,
	}
	r, err := client.Search(context.Background(), req)
	require.NoError(t, err)
	buf := bytes.NewBuffer(nil)
	w := zbuf.NopFlusher(tzngio.NewWriter(buf))
	var msgs []interface{}
	r.SetOnCtrl(func(i interface{}) {
		msgs = append(msgs, i)
	})
	require.NoError(t, zbuf.Copy(w, r))
	return buf.String(), msgs
}

func searchTzng(t *testing.T, client *api.Connection, space api.SpaceID, prog string) string {
	tzng, _ := search(t, client, space, prog)
	return tzng
}

func createTempDir(t *testing.T) string {
	// Replace '/' with '-' so it doesn't try to access dirs that don't exist.
	// Apparently "/" in a filepath for windows still tries to create a
	// directory; this solution works for windows as well.
	name := strings.ReplaceAll(t.Name(), "/", "-")
	dir, err := ioutil.TempDir("", name)
	require.NoError(t, err)
	return dir
}

func newCore(t *testing.T) (*zqd.Core, *api.Connection, func()) {
	root := createTempDir(t)
	return newCoreAtDir(t, root)
}

func newCoreAtDir(t *testing.T, dir string) (*zqd.Core, *api.Connection, func()) {
	conf := zqd.Config{
		Root:   dir,
		Logger: zaptest.NewLogger(t, zaptest.Level(zap.WarnLevel)),
	}
	require.NoError(t, os.MkdirAll(dir, 0755))
	c := zqd.NewCore(conf)
	h := zqd.NewHandler(c, conf.Logger)
	ts := httptest.NewServer(h)
	return c, api.NewConnectionTo(ts.URL), func() {
		os.RemoveAll(dir)
		ts.Close()
	}
}

func writeTempFile(t *testing.T, contents string) string {
	pattern := strings.ReplaceAll(t.Name(), "/", "-")
	f, err := ioutil.TempFile("", pattern)
	require.NoError(t, err)
	defer f.Close()
	_, err = f.WriteString(contents)
	require.NoError(t, err)
	return f.Name()
}

// postSpaceLogs POSTs the provided strings as logs in to the provided space, and returns a slice of any payloads that the server sent.
func postSpaceLogs(t *testing.T, c *api.Connection, spaceID api.SpaceID, tc *ndjsonio.TypeConfig, logs ...string) []interface{} {
	var filenames []string
	for _, log := range logs {
		name := writeTempFile(t, log)
		filenames = append(filenames, name)
		defer os.Remove(name)
	}

	ctx := context.Background()
	s, err := c.LogPost(ctx, spaceID, api.LogPostRequest{Paths: filenames, JSONTypeConfig: tc})
	require.NoError(t, err)
	var payloads []interface{}
	for {
		p, err := s.Next()
		require.NoError(t, err)
		if p == nil {
			break
		}
		payloads = append(payloads, p)
	}
	return payloads
}

func testZeekLauncher(start, wait procFn) zeek.Launcher {
	return func(ctx context.Context, r io.Reader, dir string) (zeek.Process, error) {
		p := &testZeekProcess{
			ctx:    ctx,
			reader: r,
			wd:     dir,
			wait:   wait,
			start:  start,
		}
		return p, p.Start()
	}
}

type procFn func(t *testZeekProcess) error

type testZeekProcess struct {
	ctx    context.Context
	reader io.Reader
	wd     string
	start  procFn
	wait   procFn
}

func (p *testZeekProcess) Start() error {
	if p.start != nil {
		return p.start(p)
	}
	return nil
}

func (p *testZeekProcess) Wait() error {
	if p.wait != nil {
		return p.wait(p)
	}
	return nil
}

func writeLogsFn(logs []string) procFn {
	return func(t *testZeekProcess) error {
		for _, log := range logs {
			r, err := os.Open(log)
			if err != nil {
				return err
			}
			defer r.Close()
			base := filepath.Base(r.Name())
			w, err := os.Create(filepath.Join(t.wd, base))
			if err != nil {
				return err
			}
			defer w.Close()
			if _, err = io.Copy(w, r); err != nil {
				return err
			}
		}
		// drain the reader
		_, err := io.Copy(ioutil.Discard, t.reader)
		return err
	}
}
