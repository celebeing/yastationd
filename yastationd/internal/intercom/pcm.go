package intercom

import "encoding/binary"

// Framer cuts a stream of S16LE bytes (16 kHz mono, in whatever blocks the source delivers)
// into 20 ms frames.
type Framer struct{ buf []byte }

// Push appends b and emits every whole frame it completes.
func (f *Framer) Push(b []byte, emit func([]int16)) {
	f.buf = append(f.buf, b...)
	for len(f.buf) >= FrameBytes {
		s := make([]int16, FrameSamples)
		for i := range s {
			s[i] = int16(binary.LittleEndian.Uint16(f.buf[2*i:]))
		}
		emit(s)
		f.buf = f.buf[FrameBytes:]
	}
	if len(f.buf) == 0 {
		f.buf = nil // don't keep growing one backing array
	}
}

// Upsampler3 turns 16 kHz into 48 kHz S16LE (dmix's own rate, so the playout's small pipe holds
// little time) by linear interpolation: plenty for a voice band that ends at 8 kHz.
type Upsampler3 struct{ prev int16 }

// Bytes returns s at 48 kHz as S16LE bytes, carrying the last sample over to the next call.
func (u *Upsampler3) Bytes(s []int16) []byte {
	out := make([]byte, len(s)*6)
	p := int(u.prev)
	for i, v := range s {
		c := int(v)
		for k := 0; k < 3; k++ {
			x := p + (c-p)*(k+1)/3
			binary.LittleEndian.PutUint16(out[(3*i+k)*2:], uint16(int16(x)))
		}
		p = c
	}
	if len(s) > 0 {
		u.prev = s[len(s)-1]
	}
	return out
}
