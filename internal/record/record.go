// Package record encodes key/value pairs as length-prefixed byte strings.
//
// Wire format: uvarint(len(key)) key uvarint(len(value)) value
//
// Varints spend 1 byte on lengths below 128 and 2 bytes below 16384, which is
// why SQLite, LevelDB, RocksDB and Protocol Buffers all use them for lengths.
package record

import (
	"encoding/binary"
	"errors"
)

// ErrTruncated means the buffer ends in the middle of a record.
var ErrTruncated = errors.New("record: truncated")

// Append encodes key and value onto dst and returns the extended slice.
func Append(dst, key, value []byte) []byte {
	dst = binary.AppendUvarint(dst, uint64(len(key)))
	dst = append(dst, key...)
	dst = binary.AppendUvarint(dst, uint64(len(value)))
	return append(dst, value...)
}

// Decode reads one record from b. It returns the key, the value and the
// number of bytes consumed. Key and value alias b; copy them to keep them.
func Decode(b []byte) (key, value []byte, n int, err error) {
	key, n1, err := field(b)
	if err != nil {
		return nil, nil, 0, err
	}
	value, n2, err := field(b[n1:])
	if err != nil {
		return nil, nil, 0, err
	}
	return key, value, n1 + n2, nil
}

func field(b []byte) ([]byte, int, error) {
	l, n := binary.Uvarint(b)
	if n <= 0 || uint64(len(b)-n) < l {
		return nil, 0, ErrTruncated
	}
	end := n + int(l)
	return b[n:end:end], end, nil
}
