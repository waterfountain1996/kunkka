package bencode

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
)

var (
	ErrInvalidEncoding = errors.New("bencode: invalid encoding")
)

func Decode(r io.Reader) (any, error) {
	return decodeValue(bufio.NewReader(r))
}

func decodeValue(r *bufio.Reader) (any, error) {
	prefix, err := peek1(r)
	if err != nil {
		return nil, err
	}
	switch prefix {
	case 'd':
		return decodeDict(r)
	case 'l':
		return decodeList(r)
	case 'i':
		return decodeInt(r)
	default:
		if !('0' <= prefix && prefix <= '9') {
			return nil, nil
		}
		return decodeString(r)
	}
}

func decodeString(r *bufio.Reader) (string, error) {
	line, err := readUntil(r, ':')
	if err != nil {
		return "", err
	}

	length, err := strconv.ParseUint(string(line), 10, 32)
	if err != nil {
		return "", err
	}

	// TODO: Limix maximum string length
	s := make([]byte, length)
	if _, err := io.ReadFull(r, s); err != nil {
		return "", err
	}
	return string(s), nil
}

func decodeInt(r *bufio.Reader) (int64, error) {
	line, err := readUntil(r, 'e')
	if err != nil {
		return 0, err
	}
	line = line[1:]

	return strconv.ParseInt(string(line), 10, 64)
}

func decodeList(r *bufio.Reader) ([]any, error) {
	if _, err := r.Discard(1); err != nil {
		return nil, err
	}
	var items []any
	for {
		ch, err := peek1(r)
		if err != nil {
			return nil, err
		}
		if ch == 'e' {
			break
		}

		value, err := decodeValue(r)
		if err != nil {
			return nil, err
		}
		items = append(items, value)
	}
	if _, err := r.Discard(1); err != nil {
		return nil, err
	}
	return items, nil
}

func decodeDict(r *bufio.Reader) (map[string]any, error) {
	if _, err := r.Discard(1); err != nil {
		return nil, err
	}
	items := make(map[string]any)
	var lastKey string
	for {
		ch, err := peek1(r)
		if err != nil {
			return nil, err
		}
		if ch == 'e' {
			break
		}

		rawKey, err := decodeValue(r)
		if err != nil {
			return nil, err
		}
		key, ok := rawKey.(string)
		if !ok {
			return nil, fmt.Errorf("%w: non-string dict key", ErrInvalidEncoding)
		}

		if lastKey > key {
			return nil, fmt.Errorf("%w: unsorted dict", ErrInvalidEncoding)
		}

		value, err := decodeValue(r)
		if err != nil {
			return nil, err
		}
		items[key] = value
	}
	if _, err := r.Discard(1); err != nil {
		return nil, err
	}
	return items, nil
}

func readUntil(r *bufio.Reader, delim byte) ([]byte, error) {
	line, err := r.ReadSlice(delim)
	if err != nil {
		return nil, err
	}
	return line[:len(line)-1], nil
}

func peek1(r *bufio.Reader) (byte, error) {
	prefix, err := r.Peek(1)
	if err != nil {
		return 0, err
	}
	return prefix[0], nil
}
