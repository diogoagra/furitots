package crypto

import (
	"encoding/binary"
	"errors"
)

const tableSize = 4096
const maxDecompressedSize = 1024 * 1024

func qlzGetDecompressedSize(data []byte) int {
	if (data[0] & 0x02) != 0 {
		return int(binary.LittleEndian.Uint32(data[5:9]))
	}
	return int(data[2])
}

func read24(buf []byte, off int) uint32 {
	return uint32(buf[off]) | (uint32(buf[off+1]) << 8) | (uint32(buf[off+2]) << 16)
}

func hash(value uint32) uint32 {
	return ((value >> 12) ^ value) & 0xfff
}

func QlzDecompress(data []byte) (dest []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.New("QuickLZ panic")
		}
	}()

	if len(data) < 3 {
		return nil, errors.New("data too short")
	}

	flags := data[0]
	level := (flags >> 2) & 0x03
	if level != 1 {
		return nil, errors.New("QuickLZ level not supported")
	}

	headerLen := 3
	if (flags & 0x02) != 0 {
		headerLen = 9
	}

	decompressedSize := qlzGetDecompressedSize(data)
	if decompressedSize > maxDecompressedSize {
		return nil, errors.New("decompressed size exceeds max")
	}

	dest = make([]byte, decompressedSize)

	if (flags & 0x01) == 0 {
		copy(dest, data[headerLen:headerLen+decompressedSize])
		return dest, nil
	}

	hashtable := make([]int, tableSize)
	var control uint32 = 1
	sourcePos := headerLen
	destPos := 0
	nextHashed := 0

	for {
		if control == 1 {
			if sourcePos+4 > len(data) {
				return nil, errors.New("out of bounds")
			}
			control = binary.LittleEndian.Uint32(data[sourcePos:])
			sourcePos += 4
		}

		if (control & 1) != 0 {
			control >>= 1
			next := data[sourcePos]
			sourcePos++
			if sourcePos >= len(data) {
				return nil, errors.New("QuickLZ data too short for hashIdx")
			}
			hashIdx := (int(next) >> 4) | (int(data[sourcePos]) << 4)
			sourcePos++

			matchLen := int(next) & 0x0f
			if matchLen != 0 {
				matchLen += 2
			} else {
				if sourcePos >= len(data) {
					return nil, errors.New("QuickLZ data too short for matchLen")
				}
				matchLen = int(data[sourcePos])
				sourcePos++
			}

			offset := hashtable[hashIdx]

			if offset < 0 || offset+matchLen > len(dest) || destPos+matchLen > len(dest) {
				return nil, errors.New("QuickLZ corrupt data or invalid offset")
			}

			dest[destPos] = dest[offset]
			dest[destPos+1] = dest[offset+1]
			dest[destPos+2] = dest[offset+2]
			for i := 3; i < matchLen; i++ {
				dest[destPos+i] = dest[offset+i]
			}
			destPos += matchLen

			end := destPos + 1 - matchLen
			if nextHashed < end {
				nxt := read24(dest, nextHashed)
				hashtable[hash(nxt)] = nextHashed
				for i := nextHashed + 1; i < end; i++ {
					nxt = (nxt >> 8) | (uint32(dest[i+2]) << 16)
					hashtable[hash(nxt)] = i
				}
			}
			nextHashed = destPos
		} else if destPos >= decompressedSize-10 && decompressedSize >= 10 {
			for destPos < decompressedSize {
				if control == 1 {
					control = binary.LittleEndian.Uint32(data[sourcePos:])
					sourcePos += 4
				}
				control >>= 1
				dest[destPos] = data[sourcePos]
				destPos++
				sourcePos++
			}
			break
		} else {
			dest[destPos] = data[sourcePos]
			destPos++
			sourcePos++
			control >>= 1

			end := destPos - 2
			if end < 0 {
				end = 0
			}
			if nextHashed < end {
				nxt := read24(dest, nextHashed)
				hashtable[hash(nxt)] = nextHashed
				for i := nextHashed + 1; i < end; i++ {
					nxt = (nxt >> 8) | (uint32(dest[i+2]) << 16)
					hashtable[hash(nxt)] = i
				}
			}
			if nextHashed < end {
				nextHashed = end
			}
		}
	}

	return dest, nil
}
