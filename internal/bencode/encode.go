package bencode

import (
	"errors"
	"fmt"
	"io"
	"slices"
)

var ErrUnsupportedType = errors.New("bencode: unsupported type")

func Encode(w io.Writer, value any) error {
	return encodeValue(w, value)
}

func encodeValue(w io.Writer, v any) error {
	switch tt := v.(type) {
	case string:
		return encodeString(w, tt)
	case int64:
		return encodeInt(w, tt)
	case []any:
		return encodeList(w, tt)
	case map[string]any:
		return encodeDict(w, tt)
	default:
		return fmt.Errorf("%w: %T", ErrUnsupportedType, tt)
	}
}

func encodeString(w io.Writer, s string) error {
	_, err := fmt.Fprintf(w, "%d:%s", len(s), s)
	return err
}

func encodeInt(w io.Writer, i int64) error {
	_, err := fmt.Fprintf(w, "i%de", i)
	return err
}

func encodeList(w io.Writer, items []any) error {
	if _, err := w.Write([]byte{'l'}); err != nil {
		return err
	}

	for _, item := range items {
		if err := encodeValue(w, item); err != nil {
			return err
		}
	}

	if _, err := w.Write([]byte{'e'}); err != nil {
		return err
	}
	return nil
}

func encodeDict(w io.Writer, items map[string]any) error {
	if _, err := w.Write([]byte{'d'}); err != nil {
		return err
	}

	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	slices.Sort(keys)

	for _, key := range keys {
		if err := encodeValue(w, key); err != nil {
			return err
		}
		if err := encodeValue(w, items[key]); err != nil {
			return err
		}
	}

	if _, err := w.Write([]byte{'e'}); err != nil {
		return err
	}
	return nil
}
