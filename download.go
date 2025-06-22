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
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/waterfountain1996/kunkka/internal/bitfield"
	"github.com/waterfountain1996/kunkka/internal/message"
	"github.com/waterfountain1996/kunkka/internal/torrent"
)

const (
	pieceBlockSize       = 16384 // 16 KB
	maxBacklog           = 5     // Maximum requests sent to client awaiting a response
	pieceDownloadTimeout = 30 * time.Second
)

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
			Uploaded:   0,
			Downloaded: dl.downloaded.Load(),
			Left:       *dl.torrent.Info.Length - int64(dl.downloaded.Load()),
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
	dl.peerCount.Add(1)
	defer func() {
		if n := dl.peerCount.Add(-1); n == 0 {
			dl.announceCh <- struct{}{}
		}
	}()

	conn, err := dialPeer(peerAddr, dl.infohash, peerID)
	if err != nil {
		return err
	}
	p := newPeer(conn)
	defer p.close()

	msg, err := message.Read(p.r)
	if err != nil {
		return err
	}
	if msg != nil {
		if msg.ID == message.MsgBitfield {
			// TODO: Check that it is of the right size
			p.bf = msg.Payload
		} else {
			p.bf = bitfield.NewBitfield(dl.torrent.Info.NumPieces())
		}

		switch msg.ID {
		case message.MsgChoke:
			p.state |= flagChoking
		case message.MsgUnchoke:
			p.state &= ^flagChoking
		case message.MsgInterested:
			p.state |= flagInterested
		case message.MsgNotInterested:
			p.state &= ^flagInterested
		case message.MsgHave:
			index := int(binary.BigEndian.Uint32(msg.Payload))
			p.setPiece(index)
		}
	}

	if err := p.sendInterested(); err != nil {
		return fmt.Errorf("interested: %w", err)
	}

	for pieceIndex := range pieceQueue {
		if !p.hasPiece(pieceIndex) {
			pieceQueue <- pieceIndex
			continue
		}

		dw := downloadWork{
			index:  pieceIndex,
			length: int(dl.torrent.Info.PieceLength),
		}
		if length := int(*dl.torrent.Info.Length) - pieceIndex*dw.length; length < dw.length {
			dw.length = length
		}
		copy(dw.checksum[:], []byte(dl.torrent.Info.Pieces[pieceIndex*sha1.Size:]))

		data, err := downloadPiece(p, dw)
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

// Download a piece from a peer and verify its SHA1 checksum. Returned value holds the full piece.
func downloadPiece(p *peer, dw downloadWork) ([]byte, error) {
	buffer := make([]byte, dw.length)

	var state struct {
		offset     int
		downloaded int
	}

	p.conn.SetDeadline(time.Now().Add(pieceDownloadTimeout))
	defer p.conn.SetDeadline(time.Time{})

	for state.downloaded < dw.length {
		if p.state&flagChoking == 0 {
			for p.backlog < maxBacklog && state.offset < dw.length {
				blocksize := pieceBlockSize
				if sz := dw.length - state.offset; sz < blocksize {
					blocksize = sz
				}

				if err := p.sendRequest(dw.index, state.offset, blocksize); err != nil {
					return nil, err
				}

				state.offset += blocksize
			}
		}

		msg, err := message.Read(p.r)
		if err != nil {
			return nil, err
		}
		// Ignore keepalive messages
		if msg == nil {
			continue
		}
		switch msg.ID {
		case message.MsgChoke:
			p.state |= flagChoking
		case message.MsgUnchoke:
			p.state &= ^flagChoking
		case message.MsgInterested:
			p.state |= flagInterested
		case message.MsgNotInterested:
			p.state &= ^flagInterested
		case message.MsgHave:
			index := int(binary.BigEndian.Uint32(msg.Payload))
			p.setPiece(index)
		case message.MsgPiece:
			n, err := message.DecodePiece(buffer, dw.index, msg)
			if err != nil {
				return nil, err
			}
			p.backlog--
			state.downloaded += n
		}
	}

	sum := sha1.Sum(buffer)
	if !bytes.Equal(sum[:], dw.checksum[:]) {
		return nil, errors.New("sha1 checksum mismatch")
	}

	return buffer, nil
}
