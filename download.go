package main

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"iter"
	"log"
	"math/rand/v2"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/waterfountain1996/kunkka/internal/message"
	"github.com/waterfountain1996/kunkka/internal/torrent"
)

const peerDialTimeout = 3 * time.Second

const pieceBlockSize uint32 = 16384 // 16 KB

type Downloader struct {
	torrent    *torrent.Torrent
	infohash   string
	wg         sync.WaitGroup
	bufPool    sync.Pool
	peerCount  atomic.Int32
	downloaded atomic.Uint64
	pieceQueue chan int
	announceCh chan struct{}
}

func NewDownloader(t *torrent.Torrent) *Downloader {
	return &Downloader{
		torrent:  t,
		infohash: t.Info.Hash(),
		bufPool: sync.Pool{
			New: func() any {
				return make([]byte, t.Info.PieceLength)
			},
		},
		pieceQueue: make(chan int),
		announceCh: make(chan struct{}),
	}
}

func (dl *Downloader) Download() error {
	defer close(dl.pieceQueue)

	out, err := os.Create(dl.torrent.Info.Name)
	if err != nil {
		return err
	}
	defer out.Close()

	go dl.announce(out)

	for pieceIndex := range randomPieces(dl.torrent.Info.NumPieces()) {
		dl.pieceQueue <- pieceIndex
	}

	dl.wg.Wait()
	return out.Sync()
}

func (dl *Downloader) announce(out io.WriterAt) error {
	event := "started"
	for {
		log.Println("announcing")
		_, peerAddrs, err := announce(dl.torrent.AnnounceURL, announceParams{
			InfoHash:   dl.infohash,
			PeerID:     peerID,
			Port:       6881,
			Downloaded: dl.downloaded.Load(),
			Event:      event,
		})
		if err != nil {
			return err
		}

		for _, peerAddr := range peerAddrs {
			dl.wg.Add(1)
			go func() {
				defer dl.wg.Done()

				if err := dl.spawnPeer(peerAddr, dl.pieceQueue, out); err != nil {
					log.Println("peer:", err)
				}
			}()
		}

		select {
		case <-time.After(60 * time.Second):
		case <-dl.announceCh:
		}

		event = "empty"
	}
}

type pieceResult struct {
	index int
	data  []byte
}

func (dl *Downloader) spawnPeer(peerAddr string, pieceQueue chan int, dst io.WriterAt) error {
	conn, err := dialPeer(peerAddr, dl.infohash, peerID)
	if err != nil {
		return err
	}
	defer conn.Close()

	dl.peerCount.Add(1)
	defer func() {
		if n := dl.peerCount.Add(-1); n == 0 {
			dl.announceCh <- struct{}{}
		}
	}()

	peer := NewPeer(conn)

	if err := peer.SendInterested(); err != nil {
		return fmt.Errorf("interested: %w", err)
	}

	for pieceIndex := range pieceQueue {
		if err := dl.downloadPiece(peer, pieceIndex, dst); err != nil {
			pieceQueue <- pieceIndex
			return err
		}
	}

	return nil
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

	recvInfohash, _, err := readHandshake(conn)
	if err != nil {
		return nil, err
	} else if recvInfohash != infohash {
		return nil, errors.New("infohash mismatch")
	}

	return conn, nil
}

func randomPieces(n int) iter.Seq[int] {
	return func(yield func(int) bool) {
		pieces := make([]int, n)
		for i := range n {
			pieces[i] = i
		}
		rand.Shuffle(n, func(i, j int) {
			pieces[i], pieces[j] = pieces[j], pieces[i]
		})
		for _, p := range pieces {
			if !yield(p) {
				break
			}
		}
	}
}

func (dl *Downloader) downloadPiece(p *Peer, index int, dst io.WriterAt) (err error) {
	buffer := dl.getBuffer()
	defer dl.putBuffer(buffer)

	piecelen := len(buffer)

	var state struct {
		offset     uint32
		downloaded uint32
		backlog    uint
	}

	p.nc.SetDeadline(time.Now().Add(30 * time.Second))
	defer p.nc.SetDeadline(time.Time{})

	for state.downloaded < uint32(piecelen) {
		if !p.IsChoked() {
			for state.backlog < 5 && state.offset < uint32(piecelen) {
				blocksize := pieceBlockSize
				if sz := uint32(piecelen) - state.offset; sz < blocksize {
					blocksize = sz
				}

				if err := p.SendRequest(uint32(index), state.offset, blocksize); err != nil {
					return err
				}

				state.offset += blocksize
				state.backlog++
			}
		}

		err := func() error {
			for {
				msg, err := message.Read(p.r)
				if err != nil {
					return err
				}

				// Ignore keepalive messages
				if msg == nil {
					continue
				}

				switch msg.ID {
				case message.MsgChoke:
					p.choked.Store(true)
				case message.MsgUnchoke:
					p.choked.Store(false)
				case message.MsgInterested:
					p.interested.Store(true)
				case message.MsgNotInterested:
					p.interested.Store(false)
				case message.MsgPiece:
					var idx, offset uint32
					_, _ = binary.Decode(msg.Payload[0:4], binary.BigEndian, &idx)
					_, _ = binary.Decode(msg.Payload[4:8], binary.BigEndian, &offset)
					copy(buffer[offset:], msg.Payload[8:])
					state.backlog--
					state.downloaded += uint32(len(msg.Payload[8:]))
				}
				return nil
			}
		}()
		if err != nil {
			return err
		}
	}

	sum := sha1.Sum(buffer)
	hashOffset := index * sha1.Size
	if !bytes.Equal(sum[:], []byte(dl.torrent.Info.Pieces[hashOffset:hashOffset+sha1.Size])) {
		return errors.New("sha1 hash didn't match")
	}

	dl.downloaded.Add(uint64(len(buffer)))
	log.Printf("Piece %d completed (%d/%d)\n", index, dl.downloaded.Load(), *dl.torrent.Info.Length)

	_, err = dst.WriteAt(buffer, int64(index*piecelen))
	return err
}

func (dl *Downloader) getBuffer() []byte {
	buffer := dl.bufPool.Get().([]byte)
	clear(buffer)
	return buffer
}

func (dl *Downloader) putBuffer(buffer []byte) {
	dl.bufPool.Put(buffer)
}
