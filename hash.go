package sumhash

import (
	"encoding/binary"
	"hash"
	"io"
)

// Matrix is the NxM matrix A with elements in Z_q where q=2^64
type Matrix [][]uint64

// LookupTable is the precomputed sums from a matrix for every possible byte of input.
// Its dimensions are [N][M/8][256]uint64.
type LookupTable [][][256]uint64

func RandomMatrix(rand io.Reader, N int, compressionFactor int) Matrix {
	M := compressionFactor * N * 64 // bits
	A := make([][]uint64, N)
	w := make([]byte, 8)
	for i := range A {
		A[i] = make([]uint64, M)
		for j := range A[i] {
			_, err := rand.Read(w)
			if err != nil {
				panic(err)
			}
			A[i][j] = binary.LittleEndian.Uint64(w)
		}
	}
	return A
}

func (A Matrix) LookupTable() LookupTable {
	N := len(A)
	M := len(A[0])
	At := make(LookupTable, N)
	for i := range A {
		At[i] = make([][256]uint64, M/8)

		for j := 0; j < M; j += 8 {
			for b := 0; b < 256; b++ {
				At[i][j/8][b] = sumBits(A[i][j:j+8], byte(b))
			}
		}
	}
	return At
}

func sumBits(as []uint64, b byte) uint64 {
	var x uint64
	for i := 0; i < 8; i++ {
		if b<<i&0x80 > 0 {
			x += as[i]
		}
	}
	return x
}

type Compressor interface {
	Compress(dst []uint64, msg []byte)
	InputLen() int  // len(msg)
	OutputLen() int // len(dst)
}

func (A Matrix) InputLen() int  { return len(A[0]) / 8 }
func (A Matrix) OutputLen() int { return len(A) }

func (A Matrix) Compress(dst []uint64, msg []byte) {
	_ = msg[len(A[0])/8-1]
	_ = dst[len(A)-1]

	var x uint64
	for i := range A {
		x = 0
		for j := range msg {
			for b := 0; b < 8; b++ {
				if (msg[j]<<b)&0x80 > 0 {
					x += A[i][8*j+b]
				}
			}
		}
		dst[i] = x
	}
}

func (A LookupTable) InputLen() int  { return len(A[0]) }
func (A LookupTable) OutputLen() int { return len(A) }

func (A LookupTable) Compress(dst []uint64, msg []byte) {
	_ = msg[len(A[0])-1]
	_ = dst[len(A)-1]

	var x uint64
	for i := range A {
		x = 0
		for j := range A[i] {
			x += A[i][j][msg[j]]
		}
		dst[i] = x
	}
}

// digest implementation is based on https://cs.opensource.google/go/go/+/refs/tags/go1.16.6:src/crypto/sha256/sha256.go
type digest struct {
	c         Compressor
	size      int
	blockSize int

	h   []uint64
	x   []byte
	nx  int
	len uint64
}

func New(c Compressor) hash.Hash {
	d := new(digest)
	d.c = c
	d.size = d.c.OutputLen() * 8
	d.blockSize = d.c.InputLen() - d.size
	d.x = make([]byte, d.blockSize)
	d.h = make([]uint64, c.OutputLen())
	d.Reset()
	return d
}

func (d *digest) Reset() {
	for i := range d.h {
		d.h[i] = 0
	}
	d.nx = 0
	d.len = 0
}

func (d *digest) Size() int      { return d.size }
func (d *digest) BlockSize() int { return d.blockSize }

func (d *digest) Write(p []byte) (nn int, err error) {
	nn = len(p)
	d.len += uint64(nn)
	if d.nx > 0 {
		n := copy(d.x[d.nx:], p)
		d.nx += n
		if d.nx == d.blockSize {
			blocks(d, d.x[:])
			d.nx = 0
		}
		p = p[n:]
	}
	if len(p) >= d.blockSize {
		n := len(p) / d.blockSize * d.blockSize
		blocks(d, p[:n])
		p = p[n:]
	}
	if len(p) > 0 {
		d.nx = copy(d.x[:], p)
	}
	return
}

func (d *digest) copy() *digest {
	dd := &digest{
		c:         d.c,
		size:      d.size,
		blockSize: d.blockSize,
		h:         make([]uint64, len(d.h)),
		x:         make([]byte, len(d.x)),
		nx:        d.nx,
		len:       d.len,
	}
	copy(dd.h, d.h)
	copy(dd.x, d.x)
	return dd
}

func (d *digest) Sum(in []byte) []byte {
	// Make a copy of d so that caller can keep writing and summing.
	d0 := d.copy()
	hash := d0.checkSum()
	return append(in, hash[:]...)
}

func (d *digest) checkSum() []byte {
	var B uint64 = uint64(d.blockSize)
	var P uint64 = B - 8
	// Padding. Add a 1 bit and 0 bits until P bytes mod B.
	tmp := make([]byte, B)
	tmp[0] = 0x80
	if d.len%B < P {
		d.Write(tmp[0 : P-d.len%B])
	} else {
		d.Write(tmp[0 : B+P-d.len%B])
	}

	// Length in bits.
	len := d.len << 3
	binary.LittleEndian.PutUint64(tmp[0:8], len)
	d.Write(tmp[0:8])

	if d.nx != 0 {
		panic("d.nx != 0")
	}

	digest := make([]byte, d.size)
	for i := range d.h {
		binary.LittleEndian.PutUint64(digest[8*i:8*i+8], d.h[i])
	}
	return digest
}

func blocks(d *digest, data []byte) {
	msg := make([]byte, d.c.InputLen())
	for i := 0; i <= len(data)-d.blockSize; i += d.blockSize {
		for j := range d.h {
			binary.LittleEndian.PutUint64(msg[8*j:8*j+8], d.h[j])
		}
		copy(msg[d.size:d.size+d.blockSize], data[i:i+d.blockSize])
		d.c.Compress(d.h, msg)
	}
}
