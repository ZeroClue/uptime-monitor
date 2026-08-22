// Package remotewrite exports collected samples to a Prometheus-compatible
// remote-write endpoint (Prometheus, Thanos, Cortex, Mimir, Grafana Cloud).
//
// The wire format is the remote write v1 protocol: a snappy-compressed
// protobuf WriteRequest. Both encoders are implemented here in pure Go to
// keep the binary dependency-free; snappy output uses literal chunks only
// (valid blocks, no compression) since correctness matters more than wire
// size for this exporter.
package remotewrite

import (
	"encoding/binary"
	"math"
)

type Label struct {
	Name  string
	Value string
}

type Sample struct {
	Value       float64
	TimestampMs int64
}

type TimeSeries struct {
	Labels  []Label
	Samples []Sample
}

type WriteRequest struct {
	TimeSeries []TimeSeries
}

func appendUvarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func encodeUvarintField(b []byte, field int, v uint64) []byte {
	b = append(b, byte(field<<3)) // wire type 0
	return appendUvarint(b, v)
}

func encodeLengthDelim(b []byte, field int, payload []byte) []byte {
	b = append(b, byte(field<<3|2))
	b = appendUvarint(b, uint64(len(payload)))
	return append(b, payload...)
}

func encodeLabel(b []byte, l Label) []byte {
	b = encodeLengthDelim(b, 1, []byte(l.Name))
	return encodeLengthDelim(b, 2, []byte(l.Value))
}

func encodeSample(b []byte, s Sample) []byte {
	b = append(b, 0x09) // field 1, wire type 1 (fixed64)
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], math.Float64bits(s.Value))
	b = append(b, buf[:]...)
	return encodeUvarintField(b, 2, uint64(s.TimestampMs))
}

func encodeTimeSeries(b []byte, ts TimeSeries) []byte {
	var fields []byte
	for _, l := range ts.Labels {
		var lb []byte
		lb = encodeLabel(lb, l)
		fields = encodeLengthDelim(fields, 1, lb)
	}
	for _, s := range ts.Samples {
		var sample []byte
		sample = encodeSample(sample, s)
		fields = encodeLengthDelim(fields, 2, sample)
	}
	return encodeLengthDelim(b, 1, fields)
}

// EncodeWriteRequest serializes req into the remote write v1 protobuf form.
func EncodeWriteRequest(req WriteRequest) []byte {
	var out []byte
	for _, ts := range req.TimeSeries {
		out = encodeTimeSeries(out, ts)
	}
	return out
}

const snappyMaxChunkSize = 65536

// SnappyEncode compresses src as a snappy block using literal chunks only.
// Every Prometheus-compatible receiver decodes this; it just doesn't shrink.
func SnappyEncode(dst, src []byte) []byte {
	dst = appendUvarint(dst, uint64(len(src)))
	for len(src) > 0 {
		chunk := src
		if len(chunk) > snappyMaxChunkSize {
			chunk = chunk[:snappyMaxChunkSize]
		}
		n := len(chunk) - 1
		switch {
		case n < 60:
			dst = append(dst, byte(n)<<2)
		default:
			extra := 1
			for v := uint32(n); v >= 256; v >>= 8 {
				extra++
			}
			dst = append(dst, byte((59+extra)<<2))
			for i := 0; i < extra; i++ {
				dst = append(dst, byte(n>>(8*i)))
			}
		}
		dst = append(dst, chunk...)
		src = src[len(chunk):]
	}
	return dst
}
