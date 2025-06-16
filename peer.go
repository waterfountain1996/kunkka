package main

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync/atomic"

	"github.com/waterfountain1996/kunkka/internal/message"
)

const handshakeHeader = "\x13BitTorrent protocol"

func writeHandshake(w io.Writer, infohash string, peerID string) error {
	var buf [68]byte
	copy(buf[:20], []byte(handshakeHeader))
	copy(buf[28:48], []byte(infohash))
	copy(buf[48:], []byte(peerID))
	_, err := w.Write(buf[:])
	return err
}

func readHandshake(r io.Reader) (string, string, error) {
	var buf [68]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return "", "", err
	}
	if string(buf[:20]) != handshakeHeader {
		return "", "", errors.New("invalid handshake header")
	}
	infohash := string(buf[28:48])
	peerID := string(buf[48:])
	return infohash, peerID, nil
}

type Piece struct {
	Index  int
	Offset int
	Data   []byte
}

type Peer struct {
	r          *bufio.Reader
	nc         net.Conn
	choked     atomic.Bool
	interested atomic.Bool
}

func NewPeer(nc net.Conn) *Peer {
	peer := &Peer{
		r:  bufio.NewReader(nc),
		nc: nc,
	}
	peer.choked.Store(true)
	peer.interested.Store(false)
	return peer
}

func (p *Peer) IsChoked() bool {
	return p.choked.Load()
}

func (p *Peer) SendInterested() error {
	var msg [5]byte
	binary.BigEndian.PutUint32(msg[:], 1)
	msg[4] = byte(message.MsgInterested)
	_, err := p.nc.Write(msg[:])
	return err
}

func (p *Peer) SendRequest(index, offset, blocksize uint32) error {
	var msg [17]byte
	binary.BigEndian.PutUint32(msg[0:4], 13)
	msg[4] = byte(message.MsgRequest)
	binary.BigEndian.PutUint32(msg[5:9], index)
	binary.BigEndian.PutUint32(msg[9:13], offset)
	binary.BigEndian.PutUint32(msg[13:17], blocksize)
	_, err := p.nc.Write(msg[:])
	return err
}
