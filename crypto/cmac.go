package crypto

import (
	"crypto/aes"
)

const blockSize = 16

func doubleBlock(buf []byte) []byte {
	out := make([]byte, blockSize)
	var carry byte = 0
	for i := blockSize - 1; i >= 0; i-- {
		tmp := (buf[i] << 1) | carry
		out[i] = tmp & 0xff
		carry = (buf[i] >> 7) & 1
	}
	if carry != 0 {
		out[blockSize-1] ^= 0x87
	}
	ret := make([]byte, blockSize)
	copy(ret, out)
	return ret
}

func xorBuffers(a, b []byte) []byte {
	minLen := len(a)
	if len(b) < minLen {
		minLen = len(b)
	}
	out := make([]byte, minLen)
	for i := 0; i < minLen; i++ {
		out[i] = a[i] ^ b[i]
	}
	return out
}

func aesEncryptBlock(key []byte, block []byte) []byte {
	blockCipher, err := aes.NewCipher(key)
	if err != nil {
		panic("invalid aes key")
	}
	out := make([]byte, blockSize)
	blockCipher.Encrypt(out, block)
	return out
}

// Cmac implements AES-CMAC
func Cmac(key []byte, data []byte) []byte {
	zeroBlock := make([]byte, blockSize)
	L := aesEncryptBlock(key, zeroBlock)
	K1 := doubleBlock(L)
	K2 := doubleBlock(K1)

	n := len(data)
	var numBlocks int
	if n == 0 {
		numBlocks = 1
	} else {
		numBlocks = (n + blockSize - 1) / blockSize
	}

	lastBlockComplete := (n > 0 && n%blockSize == 0)

	x := make([]byte, blockSize)
	for i := 0; i < numBlocks-1; i++ {
		block := data[i*blockSize : (i+1)*blockSize]
		y := xorBuffers(x, block)
		x = aesEncryptBlock(key, y)
	}

	var lastBlock []byte
	if lastBlockComplete {
		startIndex := (numBlocks - 1) * blockSize
		lastBlock = xorBuffers(data[startIndex:], K1)
	} else {
		padded := make([]byte, blockSize)
		startIndex := (numBlocks - 1) * blockSize
		remaining := data[startIndex:]
		copy(padded, remaining)
		padded[len(remaining)] = 0x80
		lastBlock = xorBuffers(padded, K2)
	}

	y := xorBuffers(x, lastBlock)
	return aesEncryptBlock(key, y)
}
