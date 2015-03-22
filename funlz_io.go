package funlz

import (
	"bufio"
	"io"
	"log"
)

var _ = log.Print

/* this constants are defined by format and should not be changed */
const (
	wrapsize  = 0x10000000
	window    = 4096
	buffer    = 2 * window
	smallLit  = 30
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

/*
Writer is a streaming compressor.
If wrapped writer doesn't provide WriteByte method, then it is wrapped with bufio.Writer
Output stream is not checksumed. Flush is marked with zero byte.

	comp := funlz.NewWriter(my_sock)
	comp.Write(message1)
	comp.Write(message2)
	comp.Flush()
*/
type Writer struct {
	w  writeAndByteWriter
	bw *bufio.Writer

	wself      bool
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
		w.w = wb
	} else {
		w.bw = bufio.NewWriter(wr)
		w.w = w.bw
	}
	return w
}

func (w *Writer) byte2(b1, b2 byte) (err error) {
	if err = w.w.WriteByte(b1); err == nil {
		err = w.w.WriteByte(b2)
	}
	return
}

func (w *Writer) byte3(b1, b2, b3 byte) (err error) {
	if err = w.w.WriteByte(b1); err == nil {
		if err = w.w.WriteByte(b2); err == nil {
			err = w.w.WriteByte(b3)
		}
	}
	return
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
			l = (buffer - window) - (w.wpos - w.upos)
		} else {
			l = buffer - w.wpos
		}
		do_compress := true
		if ul := uint32(len(b)); ul < l {
			l = ul
			do_compress = false
		}
		if w.wpos >= wrapsize-l {
			l = wrapsize - w.wpos
			do_compress = true
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
			if w.wpos == wrapsize {
				if err = w.flush(); err != nil {
					bytes -= int((w.wpos + buffer - w.upos) % buffer)
					break
				}
			}
		}
	}
	return
}

// WriteByte provides io.ByteWriter
func (w *Writer) WriteByte(b byte) (err error) {
	if w.err != nil {
		return w.err
	}
	w.raw[w.wpos%buffer] = b
	w.wpos++
	var l uint32
	if w.upos >= window {
		/* calculate rest in circular buffer */
		l = (buffer - window) - (w.wpos - w.upos)
	} else {
		l = buffer - w.wpos
	}
	if l == 0 || w.wpos == wrapsize {
		if err = w.compress(); err != nil {
			return
		}
		if w.wpos == wrapsize {
			if err = w.flush(); err != nil {
				return
			}
		}
	}
	return
}

func (w *Writer) compress() (err error) {
	last := w.last
	upos, wpos := w.upos, w.wpos
	litlen := w.litlen
	for upos < wpos {
		cur := w.raw[upos%buffer]
		last = (last << 8) | uint32(cur)
		h := (last * somemagicconst) >> (32 - hashlog)
		if litlen < minCopy-1 {
			upos++
			if upos >= minCopy {
				w.hash[h].push(upos)
			}
			litlen++
			continue
		}
		poses := &w.hash[h]
		m := struct{ l, p, cut uint32 }{0, 0, 0}
		wind := uint32(window)
		if wind > upos {
			wind = upos
		}
		var p, lastAtP, pb, pe, ub, ue, lim uint32
		{
			p = poses[0]
			if upos-p+minCopy > wind || p == 0 {
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
			pe, ue = p+1, upos+1
			if lookbehind {
				pb, ub = p-4, upos-4
				lim = p - litlen
				if p < litlen {
					lim = 0
				} else if upos-lim > wind {
					lim = upos - wind
				}
				for pb > lim && w.raw[pb%buffer] == w.raw[ub%buffer] {
					pb--
					ub--
				}
				pb++
				ub++
			} else {
				pb, ub = p-3, upos-3
			}
			lim = ub + maxCopy
			if lim > wpos {
				lim = wpos
			}
			for ue < lim && w.raw[pe%buffer] == w.raw[ue%buffer] {
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
			if upos-p+minCopy > wind || p == 0 {
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
			pe, ue = p+1, upos+1
			if lookbehind {
				pb, ub = p-4, upos-4
				lim = p - litlen
				if p < litlen {
					lim = 0
				} else if upos-lim > wind {
					lim = upos - wind
				}
				for pb > lim && w.raw[pb%buffer] == w.raw[ub%buffer] {
					pb--
					ub--
				}
				pb++
				ub++
			} else {
				pb, ub = p-3, upos-3
			}
			lim = ub + maxCopy
			if lim > wpos {
				lim = wpos
			}
			for ue < lim && w.raw[pe%buffer] == w.raw[ue%buffer] {
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
			if hashcopy {
				for i := m.l - m.cut; i != 0; i-- {
					last = (last << 8) | uint32(w.raw[upos%buffer])
					h = (last * somemagicconst) >> (32 - hashlog)
					upos++
					w.hash[h].push(upos)
				}
			} else {
				if lookbehind && m.cut > 4 {
					cutpos := upos - m.cut
					last = uint32(w.raw[cutpos%buffer])<<24 |
						uint32(w.raw[(cutpos+1)%buffer])<<16 |
						uint32(w.raw[(cutpos+2)%buffer])<<8 |
						uint32(w.raw[(cutpos+3)%buffer])
					h = (last * somemagicconst) >> (32 - hashlog)
					w.hash[h].push(cutpos + 4)
				}
				upos += m.l - m.cut
				last = uint32(w.raw[(upos-4)%buffer])<<24 |
					uint32(w.raw[(upos-3)%buffer])<<16 |
					uint32(w.raw[(upos-2)%buffer])<<8 |
					uint32(w.raw[(upos-1)%buffer])
				h = (last * somemagicconst) >> (32 - hashlog)
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
		if err = w.w.WriteByte(byte(l)); err != nil {
			return
		}
	} else {
		if err = w.byte2((smallLit + 1), byte(l-(smallLit+1))); err != nil {
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
		err = w.byte2(byte((l-2)<<4)|hi, lo)
	} else {
		err = w.byte3((smallCopy+1-2)<<4|hi, lo, byte(l-(smallCopy+1))) /* 0xf0|hi , l-17 */
	}
	return
}

func (w *Writer) flush() error {
	if w.upos != w.wpos {
		panic("flush upos != wpos")
	}
	if w.litlen > 0 {
		w.err = w.emitLit(w.upos-w.litlen, w.litlen)
		w.litlen = 0
		if w.err != nil {
			return w.err
		}
	}
	// flush mark
	w.err = w.w.WriteByte(0)
	if w.bw != nil {
		w.bw.Flush()
	}
	/* clear state */
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
	return w.err
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

func (w *Writer) Close() (err error) {
	return w.Flush()
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

func (r *Reader) Close() error {
	if r.err == io.EOF {
		return nil
	}
	return r.err
}

// Read provides io.Reader
func (r *Reader) Read(b []byte) (bytes int, err error) {
	if r.err != nil {
		return 0, r.err
	}
	for len(b) != 0 {
		l := r.wpos - r.rpos
		if l > 0 {
			if len(b) < int(l) {
				l = uint32(len(b))
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
			if r.rpos == wrapsize {
				r.rpos = 0
				r.wpos = 0
			}
		} else if err = r.readTag(); err != nil {
			if err == io.ErrNoProgress {
				err = nil
			}
			r.err = err
			return
		}
	}
	return
}

// ReadByte provides io.ByteReader
func (r *Reader) ReadByte() (b byte, err error) {
	if r.err != nil {
		return 0, r.err
	}
	if r.wpos == r.rpos {
	Retry:
		if err = r.readTag(); err != nil {
			if err == io.ErrNoProgress {
				goto Retry
			}
			r.err = err
			return
		}
	}
	b = r.raw[r.rpos%buffer]
	r.rpos++
	if r.rpos == wrapsize {
		r.rpos = 0
		r.wpos = 0
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
	if tag == 0 {
		/* flush mark */
		return io.ErrNoProgress
	}
	if tag < 0x20 {
		/* literal */
		l = uint32(tag)
		if tag == smallLit+1 {
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
