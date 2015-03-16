/*
funlz - compression and decompression library

Writer and Reader - streaming compressor/decompressor without framing.

Format is derived from lzf but window reduced to 4096 bytes and short copy limit is 16 bytes
	small literal len=1..31:
		[len-1] + <len bytes>
	bit literal len=32..287:
		[0x1f] [len-32] + <len bytes>
	small copy len=4..16 offset=1..4096 l=len-2 off=offset-1:
		[l<<4 | off>>8] [off&0xff]
	big copy len=17..272 offset=1..4096 l=len-2 off=offset-1:
		[0xf0 | off>>8] [off&0xff] [l-17]

For performance reason, tunable parameters are constants and not exposed.
So you encouraged to copy this library to your project and tune them.

*/
package funlz
