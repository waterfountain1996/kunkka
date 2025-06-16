package message

import (
	"encoding/binary"
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
	if messageID(msgbuf[0]) >= maxMessageID {
		return nil, fmt.Errorf("unknown message id: %#x", msgbuf[0])
	}

	msg := &Message{
		ID:      messageID(msgbuf[0]),
		Payload: msgbuf[1:],
	}
	return msg, nil
}
