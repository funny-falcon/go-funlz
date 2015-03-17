package funlz

import (
	"bufio"
	"io"
	"log"
)

var _ = log.Print

/* this constants are defined by format and should not be changed */
const (
	window    = 4096
	buffer    = 2 * window
	smallLit  = 31
	maxLit    = smallLit + 256
	minCopy   = 4
	smallCopy = 16
	maxCopy   = smallCopy + 256
	hashsize  = 1 << hashlog
)

/* mostly random const needed to compute hash */
const somemagicconst = 0x53215229

type writeAndByteWriter interface {
	io.Writer
	io.ByteWriter
}

type bytewriter interface {
	io.Writer
	byte1(b byte) error
	byte2(b1, b2 byte) error
	byte3(b1, b2, b3 byte) error
}

type bytewrite1 struct {
	io.Writer
	buf [4]byte
}

func (bw *bytewrite1) byte1(b byte) (err error) {
	bw.buf[0] = b
	_, err = bw.Write(bw.buf[:1])
	return
}

func (bw *bytewrite1) byte2(b1, b2 byte) (err error) {
	bw.buf[0] = b1
	bw.buf[1] = b2
	_, err = bw.Write(bw.buf[:2])
	return
}

func (bw *bytewrite1) byte3(b1, b2, b3 byte) (err error) {
	bw.buf[0] = b1
	bw.buf[1] = b2
	bw.buf[2] = b3
	_, err = bw.Write(bw.buf[:3])
	return
}

type bytewrite2 struct {
	writeAndByteWriter
}

func (bw *bytewrite2) byte1(b byte) (err error) {
	err = bw.WriteByte(b)
	return
}

func (bw *bytewrite2) byte2(b1, b2 byte) (err error) {
	if err = bw.WriteByte(b1); err == nil {
		err = bw.WriteByte(b2)
	}
	return
}

func (bw *bytewrite2) byte3(b1, b2, b3 byte) (err error) {
	if err = bw.WriteByte(b1); err == nil {
		if err = bw.WriteByte(b2); err == nil {
			err = bw.WriteByte(b3)
		}
	}
	return
}

/*
Writer is a streaming compressor.
It does no output buffering, so you should pass in buffered output.
Output stream is not framed and not checksumed.

	out := bufio.NewWriter(my_sock)
	comp := funlz.NewWriter(out)
	comp.Write(message1)
	comp.Write(message2)
	comp.Flush()
	out.Flush()
*/
type Writer struct {
	w          bytewriter
	err        error
	upos, wpos uint32              /* uncompressed pos and write pos in raw buffer */
	last       uint32              /* last 4 chars */
	litlen     uint32              /* lengh of last literal */
	hash       [hashsize]positions /* hash of positions */
	raw        [buffer]byte        /* input buffer */
}

// NewWriter wraps io.Writer into Writer
func NewWriter(wr io.Writer) (w *Writer) {
	w = &Writer{}
	if wb, ok := wr.(writeAndByteWriter); ok {
		w.w = &bytewrite2{writeAndByteWriter: wb}
	} else {
		w.w = &bytewrite1{Writer: wr}
	}
	return w
}

const wmask = 0x7f /* window mask */

// Write provides io.Writer
func (w *Writer) Write(b []byte) (bytes int, err error) {
	if w.err != nil {
		return 0, w.err
	}
	for len(b) != 0 {
		var l uint32
		if w.upos >= window {
			/* calculate rest in circular buffer */
			l = (w.wpos + buffer - (w.upos - window)) % buffer
		} else {
			l = buffer - w.wpos
		}
		do_compress := true
		if ul := uint32(len(b)); ul < l {
			l = ul
			do_compress = false
		}
		if w.wpos > 0xffffffff-l {
			l = 0xffffffff - w.wpos
		}
		p := w.wpos % buffer
		if p+l < buffer {
			copy(w.raw[p:p+l], b)
		} else {
			n := copy(w.raw[p:], b)
			copy(w.raw[:int(l)-n], b[n:])
		}
		b = b[l:]
		w.wpos += l
		bytes += int(l)
		if do_compress {
			if err = w.compress(); err != nil {
				bytes -= int((w.wpos + buffer - w.upos) % buffer)
				break
			}
			if w.wpos == 0xffffffff {
				if err = w.flush(); err != nil {
					bytes -= int((w.wpos + buffer - w.upos) % buffer)
					break
				}
				w.clear()
			}
		}
	}
	return
}

func (w *Writer) compress() (err error) {
	last := w.last
	upos, wpos := w.upos, w.wpos
	litlen := w.litlen
	for upos < 3 && upos < wpos {
		last = (last << 8) | uint32(w.raw[upos])
		litlen++
		upos++
	}
	for upos < wpos {
		cur := w.raw[upos%buffer]
		last = (last << 8) | uint32(cur)
		h := (last * somemagicconst) >> (32 - hashlog)
		if litlen < minCopy-1 {
			upos++
			w.hash[h].push(upos)
			litlen++
			continue
		}
		poses := &w.hash[h]
		m := struct{ l, p, cut uint32 }{0, 0, 0}
		var p, lastAtP, pb, pe, ub, ue, lim uint32
		{
			p = poses[0]
			if upos-p+litlen > window || p == 0 {
				goto LoopEnd
			}
			p--
			if w.raw[p%buffer] != cur {
				goto Loop
			}
			lastAtP = uint32(cur) | uint32(w.raw[(p-1)%buffer])<<8 |
				uint32(w.raw[(p-2)%buffer])<<16 | uint32(w.raw[(p-3)%buffer])<<24
			if lastAtP != last {
				goto Loop
			}
			pb, pe = p-4, p+1
			ub, ue = upos-4, upos+1
			lim = p - litlen
			if p < litlen {
				lim = 0
			}
			for pb > lim && w.raw[pb%buffer] == w.raw[ub%buffer] {
				pb--
				ub--
			}
			pb++
			ub++
			lim = ue - ub + maxCopy
			if lim > wpos {
				lim = wpos
			}
			for ue < wpos && w.raw[pe%buffer] == w.raw[ue%buffer] {
				ue++
				pe++
			}
			m.l = pe - pb
			m.p = pb
			m.cut = p + 1 - pb
		}
	Loop:
		for i := 1; i < len(poses); i++ {
			p = poses[i]
			/* insert new position and shift stored */
			if upos-p+litlen > window || p == 0 {
				break
			}
			p--
			if w.raw[p%buffer] != cur {
				continue
			}
			lastAtP = uint32(cur) | uint32(w.raw[(p-1)%buffer])<<8 |
				uint32(w.raw[(p-2)%buffer])<<16 | uint32(w.raw[(p-3)%buffer])<<24
			if lastAtP != last {
				continue
			}
			pb, pe = p-4, p+1
			ub, ue = upos-4, upos+1
			lim = p - litlen
			if p < litlen {
				lim = 0
			}
			for pb > lim && w.raw[pb%buffer] == w.raw[ub%buffer] {
				pb--
				ub--
			}
			pb++
			ub++
			lim = ue - ub + maxCopy
			if lim > wpos {
				lim = wpos
			}
			for ue < wpos && w.raw[pe%buffer] == w.raw[ue%buffer] {
				ue++
				pe++
			}
			if m.l < pe-pb {
				m.l = pe - pb
				m.p = pb
				m.cut = p + 1 - pb
			}
		}
	LoopEnd:
		upos++
		poses.push(upos)
		litlen++
		if m.l < minCopy {
			if litlen == maxLit+minCopy {
				if err = w.emitLit(upos-litlen, maxLit); err != nil {
					upos -= litlen
					litlen = 0
					break
				}
				litlen = minCopy
			}
		} else {
			if litlen > m.cut {
				if err = w.emitLit(upos-litlen, litlen-m.cut); err != nil {
					upos -= litlen
					litlen = 0
					break
				}
			}
			litlen = 0
			if err = w.emitCopy(upos-m.cut-m.p, m.l); err != nil {
				break
			}
			for i := m.l - m.cut; i != 0; i-- {
				last = (last << 8) | uint32(w.raw[upos%buffer])
				h = (last * somemagicconst) >> (32 - hashlog)
				upos++
				w.hash[h].push(upos)
			}
		}
	}
	w.upos = upos
	w.litlen = litlen
	w.last = last
	w.err = err
	return
}

func (w *Writer) emitLit(pos, l uint32) (err error) {
	if l <= smallLit {
		if err = w.w.byte1(byte(l - 1)); err != nil {
			return
		}
	} else {
		if err = w.w.byte2((smallLit + 1 - 1), byte(l-(smallLit+1))); err != nil {
			return
		}
	}
	rpos := pos % buffer
	var n int
	if rpos+l <= buffer {
		_, err = w.w.Write(w.raw[rpos : rpos+l])
	} else if n, err = w.w.Write(w.raw[rpos:]); err == nil {
		_, err = w.w.Write(w.raw[:int(l)-n])
	}
	return
}

func (w *Writer) emitCopy(off, l uint32) (err error) {
	off--
	hi, lo := byte(off>>8), byte(off)
	if l <= smallCopy {
		err = w.w.byte2(byte((l-2)<<4)|hi, lo)
	} else {
		err = w.w.byte3((smallCopy+1-2)<<4|hi, lo, byte(l-(smallCopy+1))) /* 0xf0|hi , l-17 */
	}
	return
}

func (w *Writer) flush() (err error) {
	if w.litlen > 0 {
		err = w.emitLit(w.upos-w.litlen, w.litlen)
		w.err = err
		w.litlen = 0
	}
	return
}

func (w *Writer) clear() {
	for i := range w.hash {
		p := &w.hash[i]
		for j := range p {
			p[j] = 0
		}
	}
	w.upos = 0
	w.wpos = 0
	w.litlen = 0
	w.last = 0
}

// Flush writes all unwritten data to output. Returns error encounted during writting.
func (w *Writer) Flush() (err error) {
	if w.err != nil {
		return w.err
	}
	if err = w.compress(); err != nil {
		return
	}
	return w.flush()
}

type readAndByteReader interface {
	io.Reader
	io.ByteReader
}

/*
Reader is a streaming decompressor.
Input is tested to have ReadByte method. If it has no, then input is wrapped into bufio.NewReader.
*/
type Reader struct {
	r          readAndByteReader
	err        error
	rpos, wpos uint32
	raw        [buffer]byte /* uncompressed data */
}

// NewReader wraps io.Reader into Reader
// If input provides ReadByte, then it is not wrapped by bufio.Reader
func NewReader(rd io.Reader) (r *Reader) {
	r = &Reader{}
	if rb, ok := rd.(readAndByteReader); ok {
		r.r = rb
	} else {
		r.r = bufio.NewReader(rd)
	}
	return
}

// Read provides io.Reader
func (r *Reader) Read(b []byte) (bytes int, err error) {
	if r.err != nil {
		return 0, r.err
	}
	for len(b) != 0 {
		l := r.wpos - r.rpos
		if l > 0 {
			if ul := uint32(len(b)); ul < l {
				l = ul
			}
			p := r.rpos % buffer
			if p+l <= buffer {
				copy(b, r.raw[p:p+l])
			} else {
				n := copy(b, r.raw[p:])
				copy(b[n:], r.raw[:int(l)-n])
			}
			bytes += int(l)
			b = b[l:]
			r.rpos += l
			if r.rpos == 0xffffffff {
				r.rpos = 0
				r.wpos = 0
			}
		} else if r.err = r.readTag(); r.err != nil {
			return
		}
	}
	return
}

func (r *Reader) readTag() (err error) {
	var tag, add, low byte
	var l uint32
	tag, err = r.r.ReadByte()
	if err != nil {
		return
	}
	if tag < 0x20 {
		/* literal */
		l = uint32(tag + 1)
		if tag == 0x1f {
			add, err = r.r.ReadByte()
			if err != nil {
				return
			}
			l += uint32(add)
		}
		p := r.wpos % buffer
		if p+l <= buffer {
			if _, err = io.ReadFull(r.r, r.raw[p:p+l]); err != nil {
				return
			}
		} else {
			var n int
			if n, err = io.ReadFull(r.r, r.raw[p:]); err != nil {
				return
			}
			if _, err = io.ReadFull(r.r, r.raw[:int(l)-n]); err != nil {
				return
			}
		}
		r.wpos += l
	} else {
		low, err = r.r.ReadByte()
		if err != nil {
			return
		}
		off := (uint32((tag&0x0f))<<8 | uint32(low)) + 1
		l = uint32((tag >> 4) + 2)
		if tag>>4 == smallCopy-1 {
			add, err = r.r.ReadByte()
			if err != nil {
				return
			}
			l += uint32(add)
		}
		p := r.wpos % buffer
		f := (r.wpos - off) % buffer
		for off < l {
			r.copyN(f, p, off)
			l -= off
			r.wpos += off
			p = r.wpos % buffer
			off *= 2
		}
		r.copyN(f, p, l)
		r.wpos += l
	}
	return
}

func (r *Reader) copyN(f, p, n uint32) {
	for n != 0 {
		t := n
		if f+t > buffer {
			t = buffer - f
		}
		if p+t > buffer {
			t = buffer - p
		}
		copy(r.raw[p:p+t], r.raw[f:f+t])
		f = (f + t) % buffer
		p = (p + t) % buffer
		n = n - t
	}
}
