package main

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"fmt"
	"iter"
	"log"
	"math/rand/v2"
	"net"
	"os"
	"sync"
	"time"

	"github.com/waterfountain1996/kunkka/internal/message"
	"github.com/waterfountain1996/kunkka/internal/torrent"
)

const peerDialTimeout = 3 * time.Second

const pieceBlockSize uint32 = 16384 // 16 KB

type Downloader struct {
	torrent  *torrent.Torrent
	infohash string
	wg       sync.WaitGroup
	bufPool  sync.Pool
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
	}
}

func (dl *Downloader) Download() error {
	_, peerAddrs, err := announce(dl.torrent.AnnounceURL, announceParams{
		InfoHash: dl.infohash,
		PeerID:   peerID,
		Port:     6881,
		Event:    "started",
	})
	if err != nil {
		return err
	}

	out, err := os.Create(dl.torrent.Info.Name)
	if err != nil {
		return err
	}
	defer out.Close()

	pieceQueue := make(chan int)
	defer close(pieceQueue)

	results := make(chan pieceResult)

	for _, peerAddr := range peerAddrs {
		dl.wg.Add(1)
		go func() {
			defer dl.wg.Done()

			if err := dl.spawnPeer(peerAddr, pieceQueue, results); err != nil {
				log.Println("peer:", err)
			}
		}()
	}

	dl.wg.Add(1)
	go func() {
		dl.wg.Done()

		for res := range results {
			offset := int64(res.index) * dl.torrent.Info.PieceLength
			if _, err := out.WriteAt(res.data, offset); err != nil {
				log.Fatalln("write:", err)
			}
			dl.putBuffer(res.data)
		}
	}()

	for pieceIndex := range randomPieces(dl.torrent.Info.NumPieces()) {
		pieceQueue <- pieceIndex
	}

	dl.wg.Wait()
	return out.Sync()
}

type pieceResult struct {
	index int
	data  []byte
}

func (dl *Downloader) spawnPeer(peerAddr string, pieceQueue <-chan int, results chan<- pieceResult) error {
	conn, err := net.DialTimeout("tcp", peerAddr, peerDialTimeout)
	if err != nil {
		return err
	}
	defer conn.Close()

	log.Printf("connected to peer %s\n", conn.RemoteAddr())

	peer := NewPeer(conn)
	if err := writeHandshake(conn, dl.infohash, peerID); err != nil {
		return fmt.Errorf("handshake: %w", err)
	}

	if recvInfohash, _, err := readHandshake(peer.r); err != nil || dl.infohash != recvInfohash {
		return err
	}

	if err := peer.SendInterested(); err != nil {
		return fmt.Errorf("interested: %w", err)
	}

	for pieceIndex := range pieceQueue {
		data, err := dl.downloadPiece(peer, pieceIndex)
		if err != nil {
			return err
		}

		sum := sha1.Sum(data)
		hashOffset := pieceIndex * sha1.Size
		if !bytes.Equal(sum[:], []byte(dl.torrent.Info.Pieces[hashOffset:hashOffset+sha1.Size])) {
			dl.putBuffer(data)
			return errors.New("sha1 hash didn't match")
		}

		results <- pieceResult{
			index: pieceIndex,
			data:  data,
		}
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

func (dl *Downloader) downloadPiece(p *Peer, index int) (buffer []byte, err error) {
	buffer = dl.getBuffer()
	piecelen := len(buffer)

	defer func() {
		if err != nil {
			dl.putBuffer(buffer)
		}
	}()

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
					return nil, err
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
			return nil, err
		}
	}
	return buffer, nil
}

func (dl *Downloader) getBuffer() []byte {
	buffer := dl.bufPool.Get().([]byte)
	clear(buffer)
	return buffer
}

func (dl *Downloader) putBuffer(buffer []byte) {
	dl.bufPool.Put(buffer)
}
