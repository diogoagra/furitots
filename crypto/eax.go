package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/subtle"
)

func eaxOmac(key []byte, tag byte, data []byte) []byte {
	tagBlock := make([]byte, 16)
	tagBlock[15] = tag
	input := append(tagBlock, data...)
	return Cmac(key, input)
}

func EaxEncrypt(key []byte, nonce []byte, header []byte, plaintext []byte, macLen int) (ciphertext []byte, mac []byte) {
	N := eaxOmac(key, 0, nonce)
	H := eaxOmac(key, 1, header)

	block, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}

	stream := cipher.NewCTR(block, N)
	ciphertext = make([]byte, len(plaintext))
	stream.XORKeyStream(ciphertext, plaintext)

	C := eaxOmac(key, 2, ciphertext)

	tag := xorBuffers(xorBuffers(N, H), C)

	return ciphertext, tag[:macLen]
}

func EaxDecrypt(key []byte, nonce []byte, header []byte, ciphertext []byte, mac []byte, macLen int) ([]byte, bool) {
	// Um MAC mais curto que o esperado é um pacote inválido, não um motivo
	// para derrubar o programa (os bytes vêm da rede).
	if macLen <= 0 || len(mac) < macLen {
		return nil, false
	}
	N := eaxOmac(key, 0, nonce)
	H := eaxOmac(key, 1, header)
	C := eaxOmac(key, 2, ciphertext)

	tag := xorBuffers(xorBuffers(N, H), C)

	if subtle.ConstantTimeCompare(tag[:macLen], mac[:macLen]) != 1 {
		return nil, false
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}

	stream := cipher.NewCTR(block, N)
	plaintext := make([]byte, len(ciphertext))
	stream.XORKeyStream(plaintext, ciphertext)

	return plaintext, true
}
