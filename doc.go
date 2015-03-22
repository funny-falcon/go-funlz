/*
funlz - compression and decompression library

Writer and Reader - streaming compressor/decompressor without framing.

Format is derived from lzf but window reduced to 4096 bytes and short copy limit is 16 bytes
	flush mark
		[0]
	small literal len=1..30:
		[len] + <len bytes>
	bit literal len=31..286:
		[0x1f] [len-31] + <len bytes>
	small copy len=4..16 offset=1..4096 l=len-2 off=offset-1:
		[l<<4 | off>>8] [off&0xff]
	big copy len=17..272 offset=1..4096 l=len-2 off=offset-1:
		[0xf0 | off>>8] [off&0xff] [l-17]

For performance reason, tunable parameters are constants and not exposed.
So you encouraged to copy this library to your project and tune them.
All tunable params and functions are in doc.go .

*/
package funlz

/*
tunable consts
backref = 1 is always faster
backref/hashlog 1/11 usualy gives same compression ratio size as 2/9, but faster
backref/hashlog 4/12 gives good compression ratio, but it is slow
*/
const (
	backref = 1  /* number of backreferences for each hash slot */
	hashlog = 11 /* log2 of hash positions */
)

/* size of positions could be increased to accieve more compression */
type positions [backref]uint32

/* you should fix this function accordantly to backref value */
func (p *positions) push(u uint32) {
	//p[7] = p[6]
	//p[6] = p[5]
	//p[5] = p[4]
	//p[4] = p[3]
	//p[3] = p[2]
	//p[2] = p[1]
	//p[1] = p[0]
	p[0] = u
}
