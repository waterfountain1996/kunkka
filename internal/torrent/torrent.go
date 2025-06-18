package torrent

import (
	"bytes"
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"os"
	"unicode/utf8"

	"github.com/waterfountain1996/kunkka/internal/bencode"
)

var ErrMalformedTorrent = errors.New("torrent: malformed torrent file")

type Torrent struct {
	AnnounceURL string
	Info        *Info
}

type Info struct {
	Name        string
	PieceLength int64
	Pieces      string
	Length      *int64
	Files       []File

	encoded []byte // Bencoded dict
}

func (i *Info) NumPieces() int {
	return len(i.Pieces) / sha1.Size
}

func (i *Info) Hash() string {
	sum := sha1.Sum(i.encoded)
	return string(sum[:])
}

type File struct {
	Length int64
	Path   string
}

func FromFile(filename string) (*Torrent, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return Parse(f)
}

func Parse(r io.Reader) (*Torrent, error) {
	value, err := bencode.Decode(r)
	if err != nil {
		return nil, err
	}
	dict, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("torrent: expected bencoded dict, got %T", value)
	}
	return decodeTorrent(dict)
}

func decodeTorrent(d map[string]any) (*Torrent, error) {
	announceURL, err := func() (string, error) {
		value, ok := d["announce"]
		if !ok {
			return "", errors.New("torrent: missing announce URL")
		}
		s, ok := value.(string)
		if !ok {
			return "", ErrMalformedTorrent
		}
		return s, nil
	}()
	if err != nil {
		return nil, err
	}

	info, err := func() (*Info, error) {
		value, ok := d["info"]
		if !ok {
			return nil, errors.New("torrent: missing info")
		}
		d, ok := value.(map[string]any)
		if !ok {
			return nil, ErrMalformedTorrent
		}
		return decodeInfo(d)
	}()
	if err != nil {
		return nil, err
	}

	t := &Torrent{
		AnnounceURL: announceURL,
		Info:        info,
	}
	return t, nil
}

func decodeInfo(d map[string]any) (*Info, error) {
	var info Info
	for key, value := range d {
		switch key {
		case "name":
			s, ok := value.(string)
			if !ok || !utf8.ValidString(s) || s == "" {
				return nil, ErrMalformedTorrent
			}
			info.Name = s
		case "piece length":
			i, ok := value.(int64)
			if !ok || i <= 0 {
				return nil, ErrMalformedTorrent
			}
			info.PieceLength = i
		case "pieces":
			s, ok := value.(string)
			if !ok || s == "" || len(s)%20 != 0 {
				return nil, ErrMalformedTorrent
			}
			info.Pieces = s
		case "length":
			i, ok := value.(int64)
			if !ok || i <= 0 {
				return nil, ErrMalformedTorrent
			}
			info.Length = &i
		case "files":
			panic("TODO: multi-file torrents")
		}
	}
	if info.Name == "" {
		return nil, errors.New("torrent: missing file name")
	}
	if info.PieceLength == 0 {
		return nil, errors.New("torrent: missing piece length")
	}
	if info.Pieces == "" {
		return nil, errors.New("torrent: missing piece hashes")
	}
	if info.Length == nil {
		return nil, errors.New("torrent: missing file length")
	}

	var buf bytes.Buffer
	_ = bencode.Encode(&buf, d)
	info.encoded = buf.Bytes()

	return &info, nil
}
