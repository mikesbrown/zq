package api

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"

	"github.com/brimsec/zq/pkg/nano"
	"github.com/brimsec/zq/zio/ndjsonio"
	"github.com/brimsec/zq/zio/zjsonio"
)

type Error struct {
	Type    string      `json:"type"`
	Kind    string      `json:"kind"`
	Message string      `json:"error"`
	Info    interface{} `json:"info,omitempty"`
}

func (e Error) Error() string {
	return e.Message
}

type TaskStart struct {
	Type   string `json:"type"`
	TaskID int64  `json:"task_id"`
}

type TaskEnd struct {
	Type   string `json:"type"`
	TaskID int64  `json:"task_id"`
	Error  *Error `json:"error,omitempty"`
}

type SearchRequest struct {
	Space SpaceID         `json:"space" validate:"required"`
	Proc  json.RawMessage `json:"proc" validate:"required"`
	Span  nano.Span       `json:"span"`
	Dir   int             `json:"dir" validate:"required"`
}

type SearchRecords struct {
	Type      string           `json:"type"`
	ChannelID int              `json:"channel_id"`
	Records   []zjsonio.Record `json:"records"`
}

type SearchWarning struct {
	Type    string `json:"type"`
	Warning string `json:"warning"`
}

type SearchEnd struct {
	Type      string `json:"type"`
	ChannelID int    `json:"channel_id"`
	Reason    string `json:"reason"`
}

type SearchStats struct {
	Type       string  `json:"type"`
	StartTime  nano.Ts `json:"start_time"`
	UpdateTime nano.Ts `json:"update_time"`
	ScannerStats
}

type ScannerStats struct {
	BytesRead      int64 `json:"bytes_read"`
	BytesMatched   int64 `json:"bytes_matched"`
	RecordsRead    int64 `json:"records_read"`
	RecordsMatched int64 `json:"records_matched"`
}

type SpaceID string

type SpaceInfo struct {
	ID          SpaceID    `json:"id"`
	Name        string     `json:"name"`
	DataPath    string     `json:"data_path"`
	Span        *nano.Span `json:"span,omitempty"`
	Size        int64      `json:"size" unit:"bytes"`
	PcapSupport bool       `json:"pcap_support"`
	PcapSize    int64      `json:"pcap_size" unit:"bytes"`
	PcapPath    string     `json:"pcap_path"`
}

type StatusResponse struct {
	Ok      bool   `json:"ok"`
	Version string `json:"version"`
}

type SpacePostRequest struct {
	Name     string `json:"name"`
	DataPath string `json:"data_path"`
}

type SpacePutRequest struct {
	Name string `json:"name"`
}

type PcapPostRequest struct {
	Path string `json:"path"`
}

type PcapPostStatus struct {
	Type          string     `json:"type"`
	StartTime     nano.Ts    `json:"start_time"`
	UpdateTime    nano.Ts    `json:"update_time"`
	PcapSize      int64      `json:"pcap_total_size" unit:"bytes"`
	PcapReadSize  int64      `json:"pcap_read_size" unit:"bytes"`
	SnapshotCount int        `json:"snapshot_count"`
	Span          *nano.Span `json:"span,omitempty"`
}

type LogPostRequest struct {
	Paths          []string             `json:"paths"`
	JSONTypeConfig *ndjsonio.TypeConfig `json:"json_type_config"`
}

type LogPostWarning struct {
	Type    string `json:"type"`
	Warning string `json:"warning"`
}

type LogPostStatus struct {
	Type         string `json:"type"`
	LogTotalSize int64  `json:"log_total_size" unit:"bytes"`
	LogReadSize  int64  `json:"log_read_size" unit:"bytes"`
}

// PcapSearch are the query string args to the packet endpoint when searching
// for packets within a connection 5-tuple.
type PcapSearch struct {
	Span    nano.Span
	Proto   string `validate:"required"`
	SrcHost net.IP `validate:"required"`
	SrcPort uint16
	DstHost net.IP `validate:"required"`
	DstPort uint16
}

// ToQuery transforms a packet search into a url.Values.
func (ps *PcapSearch) ToQuery() url.Values {
	tssec, tsns := ps.Span.Ts.Split()
	dursec := int(ps.Span.Dur / 1000000000)
	durns := int(int64(ps.Span.Dur) - int64(dursec)*1000000000)
	q := url.Values{}
	q.Add("ts_sec", strconv.Itoa(int(tssec)))
	q.Add("ts_ns", strconv.Itoa(int(tsns)))
	q.Add("duration_sec", strconv.Itoa(dursec))
	q.Add("duration_ns", strconv.Itoa(durns))
	q.Add("proto", ps.Proto)
	q.Add("src_host", ps.SrcHost.String())
	q.Add("dst_host", ps.DstHost.String())
	if ps.SrcPort != 0 {
		q.Add("src_port", strconv.Itoa(int(ps.SrcPort)))
	}
	if ps.DstPort != 0 {
		q.Add("dst_port", strconv.Itoa(int(ps.DstPort)))
	}
	return q
}

// FromQuery parses a query string and populates the receiver's values.
func (ps *PcapSearch) FromQuery(v url.Values) error {
	var err error
	var tsSec, tsNs, durSec, durNs int64
	if tsSec, err = strconv.ParseInt(v.Get("ts_sec"), 10, 64); err != nil {
		return err
	}
	if tsNs, err = strconv.ParseInt(v.Get("ts_ns"), 10, 64); err != nil {
		return err
	}
	if durSec, err = strconv.ParseInt(v.Get("duration_sec"), 10, 64); err != nil {
		return err
	}
	if durNs, err = strconv.ParseInt(v.Get("duration_ns"), 10, 64); err != nil {
		return err
	}
	if v.Get("src_port") != "" {
		p, err := strconv.ParseUint(v.Get("src_port"), 10, 16)
		if err != nil {
			return err
		}
		ps.SrcPort = uint16(p)
	}
	if v.Get("dst_port") != "" {
		p, err := strconv.ParseUint(v.Get("dst_port"), 10, 16)
		if err != nil {
			return err
		}
		ps.DstPort = uint16(p)
	}
	span := nano.Span{
		Ts:  nano.Unix(tsSec, tsNs),
		Dur: nano.Duration(durSec, durNs),
	}
	ps.Span = span
	ps.Proto = v.Get("proto")
	switch ps.Proto {
	case "tcp", "udp", "icmp":
	default:
		return fmt.Errorf("unsupported proto: %s", ps.Proto)
	}
	if ps.SrcHost = net.ParseIP(v.Get("src_host")); ps.SrcHost == nil {
		return fmt.Errorf("invalid ip: %s", ps.SrcHost)
	}
	if ps.DstHost = net.ParseIP(v.Get("dst_host")); ps.DstHost == nil {
		return fmt.Errorf("invalid ip: %s", ps.DstHost)
	}
	return nil
}
