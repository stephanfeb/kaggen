package p2p

import (
	"encoding/binary"
	"fmt"
	"io"

	"google.golang.org/protobuf/proto"
)

const (
	// ChatProtocolID is the libp2p protocol identifier for kaggen chat.
	ChatProtocolID = "/kaggen/chat/1.0.0"

	// MaxMessageSize is the maximum size of a single protobuf message (1MB).
	MaxMessageSize = 1 << 20
)

// WriteMessage writes a length-prefixed protobuf message to the writer.
// Format: 4-byte big-endian length prefix followed by protobuf bytes.
func WriteMessage(w io.Writer, msg proto.Message) error {
	data, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal proto: %w", err)
	}

	if len(data) > MaxMessageSize {
		return fmt.Errorf("message too large: %d > %d", len(data), MaxMessageSize)
	}

	// Write 4-byte length prefix (big-endian).
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(data)))
	if _, err := w.Write(lenBuf); err != nil {
		return fmt.Errorf("write length: %w", err)
	}

	// Write message data.
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("write data: %w", err)
	}

	return nil
}

// ReadMessage reads a length-prefixed protobuf message from the reader.
// Format: 4-byte big-endian length prefix followed by protobuf bytes.
func ReadMessage(r io.Reader, msg proto.Message) error {
	// Read 4-byte length prefix.
	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(r, lenBuf); err != nil {
		return fmt.Errorf("read length: %w", err)
	}

	length := binary.BigEndian.Uint32(lenBuf)
	if length > MaxMessageSize {
		return fmt.Errorf("message too large: %d > %d", length, MaxMessageSize)
	}

	// Read message data.
	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return fmt.Errorf("read data: %w", err)
	}

	// Unmarshal protobuf.
	if err := proto.Unmarshal(data, msg); err != nil {
		return fmt.Errorf("unmarshal proto: %w", err)
	}

	return nil
}
