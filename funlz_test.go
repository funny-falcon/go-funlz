package funlz

import (
	"bytes"
	"compress/flate"
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
