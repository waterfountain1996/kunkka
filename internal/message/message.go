package message

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

type messageID byte

const (
	MsgChoke messageID = iota
	MsgUnchoke
	MsgInterested
	MsgNotInterested
	MsgHave
	MsgBitfield
	MsgRequest
	MsgPiece
	MsgCancel

	maxMessageID
)

func (m messageID) String() string {
	switch m {
	case MsgChoke:
		return "choke"
	case MsgUnchoke:
		return "unchoke"
	case MsgInterested:
		return "interested"
	case MsgNotInterested:
		return "not interested"
	case MsgHave:
		return "have"
	case MsgBitfield:
		return "bitfield"
	case MsgRequest:
		return "request"
	case MsgPiece:
		return "piece"
	case MsgCancel:
		return "cancel"
	}
	return "-"
}

type Message struct {
	ID      messageID
	Payload []byte
}

func Read(r io.Reader) (*Message, error) {
	var msglen uint32
	if err := binary.Read(r, binary.BigEndian, &msglen); err != nil {
		return nil, err
	}

	// Keepalive message are 0-length
	if msglen == 0 {
		return nil, nil
	}

	msgbuf := make([]byte, msglen)
	if _, err := io.ReadFull(r, msgbuf); err != nil {
		return nil, err
	}

	msgid := messageID(msgbuf[0])
	payload := msgbuf[1:]
	switch msgid {
	case MsgChoke, MsgUnchoke, MsgInterested, MsgNotInterested:
		if len(payload) > 0 {
			return nil, fmt.Errorf("unexpected payload for %q message", msgid)
		}
	case MsgHave:
		if len(payload) != 4 {
			return nil, fmt.Errorf("unexpected payload for %q message", msgid)
		}
	case MsgRequest, MsgCancel:
		if len(payload) != 12 {
			return nil, fmt.Errorf("unexpected payload for %q message", msgid)
		}
	case MsgBitfield, MsgPiece:
		// These we cannot validate at this point
	default:
		return nil, fmt.Errorf("unknown message id: %#x", msgbuf[0])
	}

	msg := &Message{
		ID:      msgid,
		Payload: payload,
	}
	return msg, nil
}

func DecodePiece(dst []byte, idx int, m *Message) (int, error) {
	if len(m.Payload) < 8 {
		return 0, errors.New("message payload too short")
	}

	if i := binary.BigEndian.Uint32(m.Payload); int(i) != idx {
		return 0, fmt.Errorf("unexpected piece index: %d", i)
	}

	offset := int(binary.BigEndian.Uint32(m.Payload[4:]))
	if offset >= len(dst) {
		return 0, errors.New("offset too high")
	}

	src := m.Payload[8:]
	if offset+len(src) > len(dst) {
		return 0, errors.New("piece too large")
	}

	n := copy(dst[offset:], src)
	return n, nil
}
