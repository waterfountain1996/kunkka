package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"

	"github.com/waterfountain1996/kunkka/internal/bencode"
)

type announceParams struct {
	InfoHash   string
	PeerID     string
	Port       uint16
	Uploaded   int64
	Downloaded int64
	Left       int64
	Event      string // started, completed, or stopped
}

func announce(trackerURL string, params announceParams) (int64, []string, error) {
	q := &url.Values{}
	q.Set("info_hash", params.InfoHash)
	q.Set("peer_id", params.PeerID)
	if ev := params.Event; ev != "" {
		q.Set("event", ev)
	}
	u := trackerURL + "?" + q.Encode()
	res, err := http.Get(u)
	if err != nil {
		return 0, nil, nil
	}
	defer res.Body.Close()

	return decodeAnnounceResponse(res.Body)
}

func decodeAnnounceResponse(r io.Reader) (int64, []string, error) {
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
			raw, ok := value.([]any)
			if !ok {
				return 0, nil, errors.New("malformed tracker response")
			}
			peers = make([]string, len(raw))
			for i, rawItem := range raw {
				peerDict := rawItem.(map[string]any)
				peerIP := peerDict["ip"].(string)
				peerPort := peerDict["port"].(int64)
				peers[i] = net.JoinHostPort(peerIP, strconv.FormatInt(peerPort, 10))
			}
		case "failure reason":
			if errmsg, ok := value.(string); ok && errmsg != "" {
				return 0, nil, fmt.Errorf("announce query failed: %s", errmsg)
			}
			return 0, nil, errors.New("malformed tracker response")
		}
	}

	return interval, peers, nil
}
