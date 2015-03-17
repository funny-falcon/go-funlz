package funlz

import (
	"bytes"
	"compress/flate"
	"hash/crc32"
	"io"
	"io/ioutil"
	"log"
	"testing"
)

var _ = log.Printf

func compress(in []byte) []byte {
	var out bytes.Buffer
	c := NewWriter(&out)
	c.Write(in)
	c.Flush()
	return out.Bytes()
}

func compressNull(in []byte) {
	c := NewWriter(ioutil.Discard)
	c.Write(in)
	c.Flush()
}

func inflate(in []byte) []byte {
	var out bytes.Buffer
	c, _ := flate.NewWriter(&out, 1)
	c.Write(in)
	c.Flush()
	return out.Bytes()
}

func inflateNull(in []byte) {
	c, _ := flate.NewWriter(ioutil.Discard, 1)
	c.Write(in)
	c.Flush()
}

func decompress(in []byte) []byte {
	inb := bytes.NewBuffer(in)
	d := NewReader(inb)
	out, _ := ioutil.ReadAll(d)
	return out
}

func deflate(in []byte) []byte {
	inb := bytes.NewBuffer(in)
	d := flate.NewReader(inb)
	out, _ := ioutil.ReadAll(d)
	return out
}

type writeAndFlusher interface {
	io.Writer
	Flush() error
}

func compByPart(c writeAndFlusher, n int) {
	rnd := uint32(0)
	p := uint32(0)
	for i := 0; i < n; i++ {
		rnd = rnd*5 + 1
		c.Write(original[p : p+rnd%128])
		p += rnd % 128
		p = p % (1 << 18)
	}
}

type circBuffReader struct {
	b   []byte
	pos int
}

func (c *circBuffReader) Read(b []byte) (int, error) {
	bytes := 0
	for len(b) != 0 {
		n := copy(b, c.b[c.pos:])
		b = b[n:]
		c.pos += n
		if c.pos == len(c.b) {
			c.pos = 0
		}
		bytes += n
	}
	return bytes, nil
}

type circDecomp struct {
	mk func(io.Reader) io.Reader
	b  []byte
	r  io.Reader
}

func (c *circDecomp) Read(b []byte) (int, error) {
	if c.r == nil {
		c.r = c.mk(bytes.NewReader(c.b))
	}
	bytes, err := c.r.Read(b)
	if err == nil {
		c.r = nil
	}
	return bytes, nil
}

var buf [128]byte

func decompByPart(u io.Reader, n int) {
	rnd := uint32(0)
	for i := 0; i < n; i++ {
		rnd = rnd*5 + 1
		u.Read(buf[:rnd%128])
	}
}

func eq(a, b []byte) int {
	if len(a) != len(b) {
		return -2
	}
	for i, c := range a {
		if c != b[i] {
			log.Print("!eq ", c, b[i], a[i-1], b[i-1], a[i+1], b[i+1])
			return i
		}
	}
	return -1
}

var patterns [][2][]byte

func add(raw, comp string) {
	patterns = append(patterns, [2][]byte{[]byte(raw), []byte(comp)})
}

func init() {
	add("asdfasdf", "\x03asdf\x20\x03")
	add("aaaaaaaa", "\x00a\x50\x00")
	add("aaaaaaaab", "\x00a\x50\x00\x00b")
	add("baaaaaaaab", "\x01ba\x50\x00\x00b")
	if backref > 1 {
		add("baaaaacaaaaaaaaaaaaaaaaaaaaaaaaaaaab", "\x01ba\x20\x00\x00c\x30\x05\xf0\x00\x06\x00b")
		add("baaaaacaaaaaaaaaaaaaaaaaaaaaaaaaaab", "\x01ba\x20\x00\x00c\x30\x05\xf0\x00\x05\x00b")
	} else {
		add("baaaaacaaaaaaaaaaaaaaaaaaaaaaaaaaaab", "\x01ba\x20\x00\x00c\x20\x04\xf0\x00\x07\x00b")
		add("baaaaacaaaaaaaaaaaaaaaaaaaaaaaaaaab", "\x01ba\x20\x00\x00c\x20\x04\xf0\x00\x06\x00b")
	}
	add("This is a new era of my life with all good things", "\x1f\x11This is a new era of my life with all good things")
	add("This is a new era of my life with all good things. That is my new life.", "\x1f\x17This is a new era of my life with all good things. That\x20\x32\x01my\x30\x33\x20\x29\x00.")
}

var original, original1, compressed, compressed11111, flatted, flatted11111 []byte

func init() {
	original, _ = ioutil.ReadFile("GettingReal.html")
	original1, _ = ioutil.ReadFile("GettingReal.html")
	compressed = compress(original)
	compressed11111 = compress(original[:11111])
	flatted = inflate(original)
	flatted11111 = inflate(original[:11111])
}

var crc32c = crc32.MakeTable(crc32.Castagnoli)

func TestWriter(t *testing.T) {
	for _, p := range patterns {
		u, c := p[0], p[1]
		o := compress(u)
		if eq(c, o) != -1 {
			t.Errorf("not equal\n%#v\n%#v", c, o)
		}
	}
}

func TestReader(t *testing.T) {
	for _, p := range patterns {
		u, c := p[0], p[1]
		o := decompress(c)
		if eq(u, o) != -1 {
			t.Errorf("not equal\n%#v\n%#v", u, o)
		}
	}
}

func TestBigFile(t *testing.T) {
	log.Print("BigFile")
	decompressed := decompress(compressed)
	if p := eq(original, original1); p != -1 {
		t.Errorf("damage original at %d", p)
	}
	log.Printf("orig/comp %d/%d", len(original), len(compressed))
	if p := eq(original, decompressed); p != -1 {
		t.Errorf("big are not equal %d %d %d %d", len(original), len(compressed), len(decompressed), p)
	}
}

func TestHugeFile(t *testing.T) {
	var crc1, crc2 uint32
	var sz1, sz2 int
	fl := circBuffReader{b: original}
	rd, wr := io.Pipe()
	fin := make(chan struct{})
	comp := NewWriter(wr)
	decomp := NewReader(rd)
	log.Print("HugeFile")
	go func() {
		var buf [512]byte
		var last int
		for {
			n, _ := decomp.Read(buf[:])
			if n == 0 {
				break
			}
			crc2 = crc32.Update(crc2, crc32c, buf[:n])
			sz2 += n
			if sz2/1000000 > last {
				last = sz2 / 1000000
				log.Printf("hugefile: %d bytes", sz2)
			}
		}
		close(fin)
	}()
	var buf [512]byte
	for sz1 = 0; sz1 < 3*1<<29; sz1 += len(buf) {
		k, _ := fl.Read(buf[:])
		if k != 512 {
			panic("k!=512")
		}
		crc1 = crc32.Update(crc1, crc32c, buf[:])
		comp.Write(buf[:])
	}
	comp.Flush()
	wr.Close()
	<-fin
	if sz1 != sz2 {
		t.Errorf("sz1=%d sz2=%d", sz1, sz2)
	}
	if crc1 != crc2 {
		t.Errorf("crc32 mismatch")
	}
}

func BenchmarkCompressBig(b *testing.B) {
	for i := 0; i < b.N; i++ {
		compressNull(original)
	}
}

func BenchmarkCompressMedium(b *testing.B) {
	for i := 0; i < b.N; i++ {
		compressNull(original[:11111])
	}
}

func BenchmarkCompressByPart(b *testing.B) {
	c := NewWriter(ioutil.Discard)
	compByPart(c, b.N)
}

func BenchmarkDecompressBig(b *testing.B) {
	for i := 0; i < b.N; i++ {
		decompress(compressed)
	}
}

func BenchmarkDecompressMedium(b *testing.B) {
	for i := 0; i < b.N; i++ {
		decompress(compressed11111)
	}
}

func BenchmarkDecompressByPart(b *testing.B) {
	u := &circDecomp{mk: func(r io.Reader) io.Reader { return NewReader(r) }, b: compressed}
	decompByPart(u, b.N)
}

func BenchmarkFlateBig(b *testing.B) {
	for i := 0; i < b.N; i++ {
		inflateNull(original)
	}
}

func BenchmarkFlateMedium(b *testing.B) {
	for i := 0; i < b.N; i++ {
		inflateNull(original[:11111])
	}
}

func BenchmarkFlateByPart(b *testing.B) {
	c, _ := flate.NewWriter(ioutil.Discard, 1)
	compByPart(c, b.N)
}

func BenchmarkDeflateBig(b *testing.B) {
	for i := 0; i < b.N; i++ {
		deflate(flatted)
	}
}

func BenchmarkDeflateMedium(b *testing.B) {
	for i := 0; i < b.N; i++ {
		deflate(flatted11111)
	}
}

func BenchmarkDeflatePart(b *testing.B) {
	u := &circDecomp{mk: func(r io.Reader) io.Reader { return flate.NewReader(r) }, b: flatted}
	decompByPart(u, b.N)
}
