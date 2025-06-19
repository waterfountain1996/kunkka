package main

import (
	"bufio"
	"errors"
	"io"
	"net"
	"time"

	"github.com/waterfountain1996/kunkka/internal/message"
)

const peerDialTimeout = 3 * time.Second

const handshakeHeader = "\x13BitTorrent protocol"

func writeHandshake(w io.Writer, infohash string, peerID string) error {
	var buf [68]byte
	copy(buf[:20], []byte(handshakeHeader))
	copy(buf[28:48], []byte(infohash))
	copy(buf[48:], []byte(peerID))
	_, err := w.Write(buf[:])
	return err
}

func readHandshake(r io.Reader, expectInfohash string) error {
	var buf [68]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return err
	}
	if string(buf[:20]) != handshakeHeader {
		return errors.New("invalid handshake header")
	}
	if infohash := string(buf[28:48]); infohash != expectInfohash {
		return errors.New("infohash mismatch")
	}
	return nil
}

type peerState int

const (
	flagChoking      peerState = 0b0001 // Peer is choking our client
	flagInterested   peerState = 0b0010 // Peer is interested in our client
	flagAmChoking    peerState = 0b0100 // Our client is choking the peer
	flagAmInterested peerState = 0b1000 // Our client is interested in the peer
)

type peer struct {
	r     *bufio.Reader
	conn  net.Conn
	state peerState
	// Number of requests sent awaiting a reply.
	// Incremented automatically by sendRequest and recvPiece.
	backlog int
}

func newPeer(conn net.Conn) *peer {
	return &peer{
		r:     bufio.NewReader(conn),
		conn:  conn,
		state: flagChoking | flagAmChoking,
	}
}

func dialPeer(addr string, infohash string, peerID string) (conn net.Conn, err error) {
	conn, err = net.DialTimeout("tcp", addr, peerDialTimeout)
	if err != nil {
		return nil, err
	}
	defer func(conn net.Conn) {
		if err != nil {
			conn.Close()
		}
	}(conn)

	if err := writeHandshake(conn, infohash, peerID); err != nil {
		return nil, err
	}

	if err := readHandshake(conn, infohash); err != nil {
		return nil, err
	}

	return conn, nil
}

func (p *peer) sendInterested() error {
	p.state |= flagAmInterested
	msg := message.Message{ID: message.MsgInterested}
	_, err := p.conn.Write(msg.Pack())
	return err
}

func (p *peer) sendRequest(index, offset, blocksize int) error {
	p.backlog++
	msg := message.FormatRequest(index, offset, blocksize)
	_, err := p.conn.Write(msg.Pack())
	return err
}

func (p *peer) close() error {
	return p.conn.Close()
}
