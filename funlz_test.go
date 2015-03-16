package funlz

import (
	"bytes"
	//"compress/flate"
	//"io"
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

func decompress(in []byte) []byte {
	inb := bytes.NewBuffer(in)
	d := NewReader(inb)
	out, _ := ioutil.ReadAll(d)
	return out
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

var patterns = [][2][]byte{
	[2][]byte{[]byte("asdfasdf"), []byte("\x03asdf\x20\x03")},
	[2][]byte{[]byte("aaaaaaaa"), []byte("\x00a\x50\x00")},
	[2][]byte{[]byte("aaaaaaaab"), []byte("\x00a\x50\x00\x00b")},
	[2][]byte{[]byte("baaaaaaaab"), []byte("\x01ba\x50\x00\x00b")},
	[2][]byte{[]byte("baaaaacaaaaaaaaaaaaaaaaaaaaaaaaaaaab"), []byte("\x01ba\x20\x00\x00c\x30\x05\xf0\x00\x06\x00b")},
	[2][]byte{[]byte("baaaaacaaaaaaaaaaaaaaaaaaaaaaaaaaab"), []byte("\x01ba\x20\x00\x00c\x30\x05\xf0\x00\x05\x00b")},
	[2][]byte{
		[]byte("This is a new era of my life with all good things"),
		[]byte("\x1f\x11This is a new era of my life with all good things"),
	},
	[2][]byte{
		[]byte("This is a new era of my life with all good things. That is my new life."),
		[]byte("\x1f\x17This is a new era of my life with all good things. That\x20\x32\x01my\x30\x33\x20\x29\x00."),
	},
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
	original, _ := ioutil.ReadFile("GettingReal.html")
	original1, _ := ioutil.ReadFile("GettingReal.html")
	compressed := compress(original)
	decompressed := decompress(compressed)
	if p := eq(original, original1); p != -1 {
		t.Errorf("damage original at %d", p)
	}
	if p := eq(original, decompressed); p != -1 {
		t.Errorf("big are not equal %d %d %d %d", len(original), len(compressed), len(decompressed), p)
	}
}
