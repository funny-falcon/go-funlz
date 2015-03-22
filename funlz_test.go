package funlz

import (
	"bytes"
	"compress/flate"
	"hash/crc32"
	"io"
	"io/ioutil"
	"log"
	"runtime"
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
			log.Printf("!eq %x %x %x %x %x %x", c, b[i], a[i-1], b[i-1], a[i+1], b[i+1])
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
	add("asdfasdf", "\x04asdf\x20\x03\x00")
	add("aaaaaaaa", "\x01a\x50\x00\x00")
	add("aaaaaaaab", "\x01a\x50\x00\x01b\x00")
	add("baaaaaaaab", "\x02ba\x50\x00\x01b\x00")
	if backref > 1 {
		add("baaaaacaaaaaaaaaaaaaaaaaaaaaaaaaaaab", "\x02ba\x20\x00\x01c\x30\x05\xf0\x00\x06\x01b\x00")
		add("baaaaacaaaaaaaaaaaaaaaaaaaaaaaaaaab", "\x02ba\x20\x00\x01c\x30\x05\xf0\x00\x05\x01b\x00")
	} else {
		add("baaaaacaaaaaaaaaaaaaaaaaaaaaaaaaaaab", "\x02ba\x20\x00\x01c\x20\x04\xf0\x00\x07\x01b\x00")
		add("baaaaacaaaaaaaaaaaaaaaaaaaaaaaaaaab", "\x02ba\x20\x00\x01c\x20\x04\xf0\x00\x06\x01b\x00")
	}
	add("This is a new era of my life with all good things", "\x1f\x12This is a new era of my life with all good things\x00")
	add("This is a new era of my life with all good things. That is my new life.", "\x1f\x18This is a new era of my life with all good things. That\x20\x32\x02my\x30\x33\x20\x29\x01.\x00")
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
	if testing.Short() {
		t.Skip("skip in short mode")
	}
	var crc1, crc2 uint32
	var sz1, sz2 int
	fl := circBuffReader{b: original}
	rd, wr := io.Pipe()
	fin := make(chan struct{})
	comp := NewWriter(wr)
	decomp := NewReader(rd)
	log.Print("HugeFile")
	const bl = 4081
	crcch := make(chan uint32)
	crceq := make(chan bool)
	var wbuf [bl]byte
	var rbuf [bl]byte
	go func() {
		var last int
		for {
			n, _ := io.ReadFull(decomp, rbuf[:])
			if n == 0 {
				break
			}
			crc2 = crc32.Update(0, crc32c, rbuf[:n])
			crc1 := <-crcch
			if crc2 == crc1 {
				crceq <- true
			} else {
				crceq <- false
				break
			}
			sz2 += n
			if sz2/1000000 > last {
				last = sz2 / 1000000
				log.Printf("hugefile: %d bytes", sz2)
			}
		}
		close(fin)
	}()
	fl.Read(make([]byte, 1000000-5000))
	for sz1 = 0; sz1 < 3*1<<28; sz1 += len(wbuf) {
		k, _ := fl.Read(wbuf[:])
		if k != len(wbuf) {
			panic("k!=512")
		}
		crc1 = crc32.Update(0, crc32c, wbuf[:])
		comp.Write(wbuf[:])
		comp.Flush()
		runtime.Gosched()
		crcch <- crc1
		if !<-crceq {
			t.Errorf("crc mismatch")
			break
		}
	}
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
