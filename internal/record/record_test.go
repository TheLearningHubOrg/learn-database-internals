package record

import (
	"bytes"
	"errors"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	var buf []byte
	buf = Append(buf, []byte("user:42"), []byte(`{"name":"Ada"}`))
	buf = Append(buf, []byte("empty"), nil)

	k, v, n, err := Decode(buf)
	if err != nil || string(k) != "user:42" || string(v) != `{"name":"Ada"}` {
		t.Fatalf("first record: %q %q %v", k, v, err)
	}
	k, v, _, err = Decode(buf[n:])
	if err != nil || string(k) != "empty" || len(v) != 0 {
		t.Fatalf("second record: %q %q %v", k, v, err)
	}
}

func TestVarintSize(t *testing.T) {
	short := Append(nil, bytes.Repeat([]byte("k"), 127), nil)
	long := Append(nil, bytes.Repeat([]byte("k"), 128), nil)
	// 127 fits in one varint byte, 128 needs two.
	if len(short) != 1+127+1 || len(long) != 2+128+1 {
		t.Fatalf("sizes %d and %d", len(short), len(long))
	}
}

func TestTruncated(t *testing.T) {
	buf := Append(nil, []byte("key"), []byte("value"))
	for cut := range len(buf) {
		if _, _, _, err := Decode(buf[:cut]); !errors.Is(err, ErrTruncated) {
			t.Fatalf("cut at %d: err = %v, want ErrTruncated", cut, err)
		}
	}
}
