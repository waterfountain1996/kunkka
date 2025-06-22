package bitfield

type Bitfield []byte

func NewBitfield(npieces int) Bitfield {
	sz := npieces / 8
	if npieces%8 != 0 {
		sz += 1
	}
	return make(Bitfield, sz)
}

func (bf Bitfield) IsSet(index int) bool {
	byteIndex := index / 8
	offset := index % 8
	return bf[byteIndex]>>(7-offset)&1 != 0
}

func (bf Bitfield) Set(index int) {
	byteIndex := index / 8
	offset := index % 8
	bf[byteIndex] |= 1 << (7 - offset)
}
