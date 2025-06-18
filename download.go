package main

import (
	"bytes"
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"iter"
	"log"
	"math/rand/v2"
	"net"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/waterfountain1996/kunkka/internal/message"
	"github.com/waterfountain1996/kunkka/internal/torrent"
)

const peerDialTimeout = 3 * time.Second

const pieceBlockSize = 16384 // 16 KB

type Downloader struct {
	torrent    *torrent.Torrent
	infohash   string
	wg         sync.WaitGroup
	peerCount  atomic.Int32
	downloaded atomic.Uint64
	pieceQueue chan int
	announceCh chan struct{}
}

func NewDownloader(t *torrent.Torrent) *Downloader {
	return &Downloader{
		torrent:    t,
		infohash:   t.Info.Hash(),
		pieceQueue: make(chan int),
		announceCh: make(chan struct{}),
	}
}

func (dl *Downloader) Download() error {
	out, err := os.Create(dl.torrent.Info.Name)
	if err != nil {
		return err
	}
	defer out.Close()

	go dl.announce(out)

	for pieceIndex := range randomPieces(dl.torrent.Info.NumPieces()) {
		dl.pieceQueue <- pieceIndex
	}

	for dl.downloaded.Load() < uint64(*dl.torrent.Info.Length) {
		runtime.Gosched()
	}
	close(dl.pieceQueue)

	dl.wg.Wait()
	return out.Sync()
}

func (dl *Downloader) announce(out io.WriterAt) error {
	event := "started"
	for dl.downloaded.Load() < uint64(*dl.torrent.Info.Length) {
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
	return nil
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
		dw := downloadWork{
			index:  pieceIndex,
			length: int(dl.torrent.Info.PieceLength),
		}
		copy(dw.checksum[:], []byte(dl.torrent.Info.Pieces[pieceIndex*sha1.Size:]))

		data, err := downloadPiece(peer, dw)
		if err != nil {
			pieceQueue <- pieceIndex
			return err
		}

		if _, err := dst.WriteAt(data, int64(pieceIndex)*dl.torrent.Info.PieceLength); err != nil {
			return err
		}

		dl.downloaded.Add(uint64(len(data)))
		log.Printf("Piece %d completed (%d/%d)\n", dw.index, dl.downloaded.Load(), *dl.torrent.Info.Length)
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

type downloadWork struct {
	index    int
	length   int
	checksum [sha1.Size]byte
}

func downloadPiece(p *Peer, dw downloadWork) ([]byte, error) {
	buffer := make([]byte, dw.length)

	var state struct {
		offset     int
		downloaded int
		backlog    int
	}

	p.nc.SetDeadline(time.Now().Add(30 * time.Second))
	defer p.nc.SetDeadline(time.Time{})

	const maxbacklog = 5
	for state.downloaded < dw.length {
		if !p.IsChoking() {
			for state.backlog < maxbacklog && state.offset < dw.length {
				blocksize := pieceBlockSize
				if sz := dw.length - state.offset; sz < blocksize {
					blocksize = sz
				}

				if err := p.SendRequest(uint32(dw.index), uint32(state.offset), uint32(blocksize)); err != nil {
					return nil, err
				}

				state.offset += blocksize
				state.backlog++
			}
		}

		// TODO: Make this a method on a Peer
		err := func() error {
			for {
				msg, err := message.Read(p.r)
				if err != nil {
					return err
				}

				if msg == nil {
					continue
				}

				// TODO: Do we really need atomics here?
				switch msg.ID {
				case message.MsgChoke:
					p.choking.Store(true)
				case message.MsgUnchoke:
					p.choking.Store(false)
				case message.MsgInterested:
					p.interested.Store(true)
				case message.MsgNotInterested:
					p.interested.Store(false)
				case message.MsgPiece:
					n, err := message.DecodePiece(buffer, dw.index, msg)
					if err != nil {
						return err
					}
					state.backlog--
					state.downloaded += n
				}
				return nil
			}
		}()
		if err != nil {
			return nil, err
		}
	}

	sum := sha1.Sum(buffer)
	if !bytes.Equal(sum[:], dw.checksum[:]) {
		return nil, errors.New("sha1 checksum mismatch")
	}

	return buffer, nil
}
