package remotewrite

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

func TestEncodeLabel(t *testing.T) {
	// Label{name: "__name__", value: "cpu.user_pct"}:
	//   field 1 (name):  tag 0x0A len 8 "__name__"
	//   field 2 (value): tag 0x12 len 12 "cpu.user_pct"
	want := []byte{
		0x0A, 0x08, '_', '_', 'n', 'a', 'm', 'e', '_', '_',
		0x12, 0x0C, 'c', 'p', 'u', '.', 'u', 's', 'e', 'r', '_', 'p', 'c', 't',
	}
	got := encodeLabel(nil, Label{Name: "__name__", Value: "cpu.user_pct"})
	if !bytes.Equal(got, want) {
		t.Errorf("label bytes:\nwant % x\n got % x", want, got)
	}
}

func TestEncodeSample(t *testing.T) {
	var valueBuf [8]byte
	binary.LittleEndian.PutUint64(valueBuf[:], math.Float64bits(42.5))
	want := append([]byte{0x09}, valueBuf[:]...)
	want = append(want, encodeUvarintField(nil, 2, 1779000000000)...)

	got := encodeSample(nil, Sample{Value: 42.5, TimestampMs: 1779000000000})
	if !bytes.Equal(got, want) {
		t.Errorf("sample bytes:\nwant % x\n got % x", want, got)
	}
}

func TestEncodeTimeSeries(t *testing.T) {
	ts := TimeSeries{
		Labels:  []Label{{Name: "__name__", Value: "up"}},
		Samples: []Sample{{Value: 1, TimestampMs: 1000}},
	}
	out := encodeTimeSeries(nil, ts)
	if len(out) == 0 || out[0] != 0x0A {
		t.Fatalf("series must start with label field tag 0x0A, got % x", out)
	}
	if !bytes.Contains(out, []byte("up")) {
		t.Error("label value missing")
	}
}

// decodeFields is a tiny independent protobuf reader used to verify the
// encoder from the consumer's side (as Prometheus would).
type field struct {
	num  int
	data []byte // length-delimited payload or raw fixed64/varint payload
	val  uint64 // decoded varint for wire type 0
	f64  float64
	typ  int // 0 varint, 1 fixed64, 2 bytes
}

func decodeFields(t *testing.T, b []byte) []field {
	t.Helper()
	var out []field
	for len(b) > 0 {
		tag, n := binary.Uvarint(b)
		if n <= 0 {
			t.Fatalf("bad tag at %d", len(b))
		}
		b = b[n:]
		fld := field{num: int(tag >> 3), typ: int(tag & 7)}
		switch fld.typ {
		case 0:
			v, n := binary.Uvarint(b)
			if n <= 0 {
				t.Fatal("bad varint")
			}
			fld.val = v
			b = b[n:]
		case 1:
			if len(b) < 8 {
				t.Fatal("short fixed64")
			}
			fld.f64 = math.Float64frombits(binary.LittleEndian.Uint64(b))
			b = b[8:]
		case 2:
			l, n := binary.Uvarint(b)
			if n <= 0 || int(l) > len(b) {
				t.Fatal("bad length")
			}
			b = b[n:]
			fld.data = b[:l]
			b = b[l:]
		default:
			t.Fatalf("unsupported wire type %d in test decoder", fld.typ)
		}
		out = append(out, fld)
	}
	return out
}

func TestEncodeWriteRequest_RoundtripThroughIndependentDecoder(t *testing.T) {
	req := WriteRequest{
		TimeSeries: []TimeSeries{
			{
				Labels: []Label{
					{Name: "__name__", Value: "cpu.user_pct"},
					{Name: "host", Value: "web-01"},
					{Name: "project", Value: "prod"},
				},
				Samples: []Sample{
					{Value: 41.5, TimestampMs: 1779000000000},
					{Value: 42.5, TimestampMs: 1779000060000},
				},
			},
			{
				Labels:  []Label{{Name: "__name__", Value: "mem.used_pct"}, {Name: "host", Value: "web-02"}},
				Samples: []Sample{{Value: 55, TimestampMs: 1779000000000}},
			},
		},
	}

	raw := EncodeWriteRequest(req)
	fields := decodeFields(t, raw)
	if len(fields) != 2 || fields[0].num != 1 || fields[1].num != 1 {
		t.Fatalf("expected two timeseries on field 1, got %+v", fields)
	}

	first := decodeFields(t, fields[0].data)
	labels := 0
	samples := 0
	for _, f := range first {
		switch {
		case f.num == 1 && f.typ == 2:
			labels++
			lf := decodeFields(t, f.data)
			if len(lf) != 2 || lf[0].typ != 2 || lf[1].typ != 2 {
				t.Fatalf("label must be two strings: %+v", lf)
			}
		case f.num == 2 && f.typ == 2:
			samples++
			sf := decodeFields(t, f.data)
			if len(sf) != 2 || sf[0].typ != 1 || sf[1].typ != 0 {
				t.Fatalf("sample must be double+varint: %+v", sf)
			}
		default:
			t.Fatalf("unexpected field num=%d typ=%d", f.num, f.typ)
		}
	}
	if labels != 3 || samples != 2 {
		t.Errorf("first series: want 3 labels 2 samples, got %d/%d", labels, samples)
	}
}

func TestEncodeSnappyBlock_Golden(t *testing.T) {
	// "abc" -> preamble varint(3), literal chunk tag ((3-1)<<2)=0x08, then bytes.
	want := []byte{0x03, 0x08, 'a', 'b', 'c'}
	if got := SnappyEncode(nil, []byte("abc")); !bytes.Equal(got, want) {
		t.Errorf("snappy(abc):\nwant % x\n got % x", want, got)
	}

	// Empty input: just a zero-length preamble.
	if got := SnappyEncode(nil, nil); !bytes.Equal(got, []byte{0x00}) {
		t.Errorf("snappy(empty): got % x", got)
	}
}

func TestEncodeSnappyBlock_LongLiteralHeader(t *testing.T) {
	// 300 bytes: len-1 = 299 = 0x12B needs two extra bytes -> tag (59+2)<<2 = 0xF4.
	src := bytes.Repeat([]byte{'x'}, 300)
	got := SnappyEncode(nil, src)
	preambleLen, n := binary.Uvarint(got)
	if n <= 0 || preambleLen != 300 {
		t.Fatalf("preamble: %d %d", preambleLen, n)
	}
	if got[n] != 0xF4 {
		t.Fatalf("literal tag with 2 extra bytes: want 0xF4 got %#x", got[n])
	}
	if got[n+1] != 0x2B || got[n+2] != 0x01 {
		t.Fatalf("literal length LE bytes: want 2B 01 got % X", got[n+1:n+3])
	}
	if !bytes.Equal(got[n+3:], src) {
		t.Error("literal payload mismatch")
	}
}

func TestEncodeSnappyBlock_ChunksLargeInput(t *testing.T) {
	src := bytes.Repeat([]byte{0xAB}, snappyMaxChunkSize+10)
	got := SnappyEncode(nil, src)
	preambleLen, n := binary.Uvarint(got)
	if preambleLen != uint64(len(src)) {
		t.Fatalf("preamble %d != %d", preambleLen, len(src))
	}

	rest := got[n:]
	chunkCount := 0
	var rebuilt []byte
	for len(rest) > 0 {
		chunkCount++
		tag := rest[0]
		if tag&3 != 0 {
			t.Fatalf("expected literal chunk tags only, got %#x", tag)
		}
		upper := tag >> 2
		var litLen int
		if upper < 60 {
			litLen = int(upper) + 1
			rest = rest[1:]
		} else {
			extra := int(upper) - 59
			var v uint64
			for i := 0; i < extra; i++ {
				v |= uint64(rest[1+i]) << (8 * i)
			}
			litLen = int(v) + 1
			rest = rest[1+extra:]
		}
		rebuilt = append(rebuilt, rest[:litLen]...)
		rest = rest[litLen:]
	}
	if chunkCount != 2 {
		t.Errorf("want 2 chunks, got %d", chunkCount)
	}
	if !bytes.Equal(rebuilt, src) {
		t.Error("rebuilt payload mismatch")
	}
}
