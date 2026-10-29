// Package voiceid is the speaker side of voice ID (docs/voice-id.md): the firmware's voiceprint
// model run by `spotter-host voice`, its gender and age classifiers, and the verdict against the
// household's voices.
package voiceid

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
)

// Classifier is one of the firmware's voiceprint classifiers (gender, age): a dense layer with
// int8 weights and a ReLU, a dense layer to one value, a sigmoid. The vendor's TFLite build has no
// LOGISTIC op in the resolver spotter-host can use, so the layers are read from the .tflite file
// and computed here.
type Classifier struct {
	w1    [][]float32 // [hidden][dim], dequantized
	b1    []float32
	w2    []float32 // [hidden]
	b2    float32
	Model string // the file it came from
}

// Predict returns the classifier's output (0..1) for voiceprint emb.
func (c *Classifier) Predict(emb []float32) float64 {
	var out float64
	for i, row := range c.w1 {
		var s float64
		for j, w := range row {
			if j < len(emb) {
				s += float64(w) * float64(emb[j])
			}
		}
		s += float64(c.b1[i])
		if s > 0 {
			out += s * float64(c.w2[i])
		}
	}
	return 1 / (1 + math.Exp(-(out + float64(c.b2))))
}

// LoadClassifier reads a classifier's layers from its .tflite file.
func LoadClassifier(path string) (*Classifier, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c, err := parseClassifier(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	c.Model = path
	return c, nil
}

// TFLite type codes (schema.fbs TensorType).
const (
	tflFloat32 = 0
	tflInt8    = 9
)

type tflTensor struct {
	shape []int
	typ   int
	data  []byte
	scale []float32
}

func parseClassifier(b []byte) (*Classifier, error) {
	ts, err := tflTensors(b)
	if err != nil {
		return nil, err
	}
	// The layers by shape: int8 [H,D] weights with one scale, float [H] bias, float [1,H] and [1].
	var w1, b1, w2, b2 *tflTensor
	for i := range ts {
		t := &ts[i]
		if len(t.data) == 0 {
			continue
		}
		switch {
		case t.typ == tflInt8 && len(t.shape) == 2 && len(t.scale) == 1:
			w1 = t
		case t.typ == tflFloat32 && len(t.shape) == 2 && t.shape[0] == 1:
			w2 = t
		case t.typ == tflFloat32 && len(t.shape) == 1 && t.shape[0] == 1:
			b2 = t
		case t.typ == tflFloat32 && len(t.shape) == 1:
			b1 = t
		}
	}
	if w1 == nil || b1 == nil || w2 == nil || b2 == nil {
		return nil, errors.New("not a two-layer classifier")
	}
	h, d := w1.shape[0], w1.shape[1]
	if b1.shape[0] != h || w2.shape[1] != h || len(w1.data) != h*d {
		return nil, errors.New("layer sizes don't match")
	}
	c := &Classifier{b1: floats(b1.data), w2: floats(w2.data), b2: floats(b2.data)[0]}
	for i := 0; i < h; i++ {
		row := make([]float32, d)
		for j := range row {
			row[j] = float32(int8(w1.data[i*d+j])) * w1.scale[0]
		}
		c.w1 = append(c.w1, row)
	}
	return c, nil
}

func floats(b []byte) []float32 {
	f := make([]float32, len(b)/4)
	for i := range f {
		f[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return f
}

// A minimal FlatBuffers reader for what parseClassifier needs of the TFLite schema: Model
// {subgraphs: 2, buffers: 4}, SubGraph {tensors: 0}, Tensor {shape: 0, type: 1, buffer: 2,
// quantization: 4}, QuantizationParameters {scale: 2}, Buffer {data: 0}.
type fbuf []byte

var errFB = errors.New("malformed tflite file")

func (b fbuf) u32(o int) (int, error) {
	if o < 0 || o+4 > len(b) {
		return 0, errFB
	}
	return int(binary.LittleEndian.Uint32(b[o:])), nil
}

// field returns the absolute offset of field i of the table at t, or 0 if absent.
func (b fbuf) field(t, i int) (int, error) {
	if t < 0 || t+4 > len(b) {
		return 0, errFB
	}
	vt := t - int(int32(binary.LittleEndian.Uint32(b[t:])))
	if vt < 0 || vt+4 > len(b) {
		return 0, errFB
	}
	vsize := int(binary.LittleEndian.Uint16(b[vt:]))
	p := vt + 4 + 2*i
	if p+2 > vt+vsize || p+2 > len(b) {
		return 0, nil
	}
	off := int(binary.LittleEndian.Uint16(b[p:]))
	if off == 0 {
		return 0, nil
	}
	return t + off, nil
}

// ref follows the uoffset at o (to a table, vector or string).
func (b fbuf) ref(o int) (int, error) {
	n, err := b.u32(o)
	return o + n, err
}

// vector returns the start and length of the vector field i of table t (0, 0 if absent).
func (b fbuf) vector(t, i int) (start, n int, err error) {
	f, err := b.field(t, i)
	if err != nil || f == 0 {
		return 0, 0, err
	}
	v, err := b.ref(f)
	if err != nil {
		return 0, 0, err
	}
	n, err = b.u32(v)
	if err != nil || v+4+n > len(b) {
		return 0, 0, errFB
	}
	return v + 4, n, nil
}

// tables returns the tables of the vector-of-tables field i of t.
func (b fbuf) tables(t, i int) ([]int, error) {
	s, n, err := b.vector(t, i)
	if err != nil {
		return nil, err
	}
	out := make([]int, n)
	for k := range out {
		if out[k], err = b.ref(s + 4*k); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func tflTensors(raw []byte) ([]tflTensor, error) {
	b := fbuf(raw)
	root, err := b.ref(0)
	if err != nil {
		return nil, err
	}
	bufs, err := b.tables(root, 4)
	if err != nil {
		return nil, err
	}
	subs, err := b.tables(root, 2)
	if err != nil || len(subs) == 0 {
		return nil, errFB
	}
	tens, err := b.tables(subs[0], 0)
	if err != nil {
		return nil, err
	}
	var out []tflTensor
	for _, t := range tens {
		var x tflTensor
		s, n, err := b.vector(t, 0)
		if err != nil {
			return nil, err
		}
		for k := 0; k < n; k++ {
			v, _ := b.u32(s + 4*k)
			x.shape = append(x.shape, v)
		}
		if f, _ := b.field(t, 1); f > 0 && f < len(b) {
			x.typ = int(b[f])
		}
		if f, _ := b.field(t, 2); f > 0 {
			bi, err := b.u32(f)
			if err == nil && bi < len(bufs) {
				if ds, dn, err := b.vector(bufs[bi], 0); err == nil {
					x.data = b[ds : ds+dn]
				}
			}
		}
		if f, _ := b.field(t, 4); f > 0 {
			if q, err := b.ref(f); err == nil {
				if ss, sn, err := b.vector(q, 2); err == nil && ss+4*sn <= len(b) {
					x.scale = floats(b[ss : ss+4*sn])
				}
			}
		}
		out = append(out, x)
	}
	return out, nil
}
