package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"time"

	"github.com/waterfountain1996/kunkka/internal/bencode"
)

type announceParams struct {
	InfoHash   string
	PeerID     string
	Port       uint16
	Uploaded   int64
	Downloaded uint64
	Left       int64
	Event      string // started, completed, stopped or empty
}

func announce(trackerURL string, params announceParams) (time.Duration, []string, error) {
	q := &url.Values{}
	q.Set("info_hash", params.InfoHash)
	q.Set("peer_id", params.PeerID)
	q.Set("port", strconv.FormatUint(uint64(params.Port), 10))
	q.Set("uploaded", strconv.FormatInt(params.Uploaded, 10))
	q.Set("downloaded", strconv.FormatUint(params.Downloaded, 10))
	q.Set("left", strconv.FormatInt(params.Left, 10))
	if ev := params.Event; ev != "" {
		q.Set("event", ev)
	}
	q.Set("compact", "1")

	u := trackerURL + "?" + q.Encode()
	res, err := http.Get(u)
	if err != nil {
		return 0, nil, nil
	}
	defer res.Body.Close()

	return decodeAnnounceResponse(res.Body)
}

func decodeAnnounceResponse(r io.Reader) (time.Duration, []string, error) {
	value, err := bencode.Decode(r)
	if err != nil {
		return 0, nil, err
	}
	d, ok := value.(map[string]any)
	if !ok {
		return 0, nil, fmt.Errorf("expected bencoded dict, got %T", value)
	}

	var (
		interval int64
		peers    []string
	)
	for key, value := range d {
		switch key {
		case "interval":
			var ok bool
			interval, ok = value.(int64)
			if !ok || interval <= 0 {
				return 0, nil, errors.New("malformed tracker response")
			}
		case "peers":
			switch tt := value.(type) {
			case []any:
				peers = make([]string, len(tt))
				for i, rawItem := range tt {
					peerDict := rawItem.(map[string]any)
					peerIP := peerDict["ip"].(string)
					peerPort := peerDict["port"].(int64)
					peers[i] = net.JoinHostPort(peerIP, strconv.FormatInt(peerPort, 10))
				}
			case string:
				if tt == "" || len(tt)%6 != 0 {
					return 0, nil, errors.New("malformed tracker response")
				}
				peers = make([]string, 0, len(tt)/6)
				for i := 0; i < len(tt); i += 6 {
					s := tt[i : i+6]
					addr, _ := netip.AddrFromSlice([]byte(s[0:4]))
					port := binary.BigEndian.Uint16([]byte(s[4:6]))
					ap := netip.AddrPortFrom(addr, port)
					peers = append(peers, ap.String())
				}
			default:
				return 0, nil, errors.New("malformed tracker response")
			}
		case "failure reason":
			if errmsg, ok := value.(string); ok && errmsg != "" {
				return 0, nil, fmt.Errorf("announce query failed: %s", errmsg)
			}
			return 0, nil, errors.New("malformed tracker response")
		}
	}

	return time.Duration(interval) * time.Second, peers, nil
}
