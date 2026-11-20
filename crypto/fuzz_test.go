package crypto

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"math/big"
	"testing"
)

// Os bytes que chegam da rede não são confiáveis: o descompressor e a
// decifragem não podem derrubar o app nem devolver mais do que o limite, seja
// qual for a entrada.

func FuzzQlzDecompress(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0x04, 0x03, 0x03})                               // nível 1, sem compressão, 3 bytes declarados e nenhum presente
	f.Add([]byte{0x05, 0x10, 0x20, 0, 0, 0, 0x80, 'a', 'b', 'c'}) // comprimido, cabeçalho curto
	f.Add([]byte{0x07, 0, 0, 0, 0, 0xff, 0xff, 0xff, 0x7f})       // cabeçalho longo declarando 2 GB
	f.Add([]byte{0x05, 0x08, 0xff, 0xff, 0xff, 0xff, 0xff, 0x00, 0x00, 0x00, 0x00, 0x00})
	f.Fuzz(func(t *testing.T, data []byte) {
		out, err := QlzDecompress(data)
		if err == nil && len(out) > maxDecompressedSize {
			t.Fatalf("saída de %d bytes, acima do limite", len(out))
		}
	})
}

func FuzzEaxDecrypt(f *testing.F) {
	key := bytes.Repeat([]byte{0x2b}, 16)
	nonce := bytes.Repeat([]byte{0x11}, 16)
	ct, mac := EaxEncrypt(key, nonce, []byte("cabecalho"), []byte("mensagem de teste"), 8)
	f.Add(key, nonce, []byte("cabecalho"), ct, mac)
	f.Add(key, nonce, []byte{}, []byte{}, []byte{})
	f.Add(key, nonce, []byte{1}, []byte{2, 3}, []byte{4})
	f.Fuzz(func(t *testing.T, key, nonce, header, ciphertext, mac []byte) {
		if len(key) != 16 || len(nonce) != 16 {
			return // quem chama sempre passa a chave e o nonce derivados, de 16 bytes
		}
		EaxDecrypt(key, nonce, header, ciphertext, mac, 8)
	})
}

// Um pacote alterado no caminho (ou forjado por quem não tem a chave) tem de
// ser recusado.
func TestEaxRejectsTampering(t *testing.T) {
	key := bytes.Repeat([]byte{0x2b}, 16)
	nonce := bytes.Repeat([]byte{0x11}, 16)
	header, plain := []byte("cabecalho"), []byte("mensagem de teste")
	ct, mac := EaxEncrypt(key, nonce, header, plain, 8)
	if got, ok := EaxDecrypt(key, nonce, header, ct, mac, 8); !ok || !bytes.Equal(got, plain) {
		t.Fatalf("a mensagem íntegra deveria ser aceita: %q %v", got, ok)
	}
	flip := func(b []byte, i int) []byte { c := append([]byte(nil), b...); c[i] ^= 1; return c }
	for i := range ct {
		if _, ok := EaxDecrypt(key, nonce, header, flip(ct, i), mac, 8); ok {
			t.Fatalf("texto cifrado alterado no byte %d foi aceito", i)
		}
	}
	for i := range mac {
		if _, ok := EaxDecrypt(key, nonce, header, ct, flip(mac, i), 8); ok {
			t.Fatalf("MAC alterado no byte %d foi aceito", i)
		}
	}
	for i := range header {
		if _, ok := EaxDecrypt(key, nonce, flip(header, i), ct, mac, 8); ok {
			t.Fatalf("cabeçalho alterado no byte %d foi aceito", i)
		}
	}
	if _, ok := EaxDecrypt(key, nonce, header, ct, mac[:4], 8); ok {
		t.Fatal("MAC curto foi aceito")
	}
	if _, ok := EaxDecrypt(key, nonce, header, ct, nil, 8); ok {
		t.Fatal("pacote sem MAC foi aceito")
	}
}

// O segredo do protocolo antigo tem de ser o mesmo que o outro lado calcula, e
// uma chave pública que não é um ponto da curva tem de ser recusada.
func TestGetSharedSecret(t *testing.T) {
	ours, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub := theirs.PublicKey().Bytes() // 0x04, X, Y
	x, y := new(big.Int).SetBytes(pub[1:33]), new(big.Int).SetBytes(pub[33:65])
	got, err := GetSharedSecret(ours, x, y)
	if err != nil {
		t.Fatal(err)
	}
	other, err := theirs.ECDH(ours.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, Sha1(other)) || len(got) != 20 {
		t.Fatalf("segredo diferente do calculado pelo outro lado: %x", got)
	}

	huge := new(big.Int).Lsh(big.NewInt(1), 300)
	for name, pt := range map[string][2]*big.Int{
		"fora da curva":   {big.NewInt(5), big.NewInt(7)},
		"ponto (0,0)":     {big.NewInt(0), big.NewInt(0)},
		"Y trocado":       {x, new(big.Int).Add(y, big.NewInt(1))},
		"coordenada >256": {huge, y},
		"negativa":        {big.NewInt(-1), y},
		"sem coordenada":  {nil, y},
	} {
		if secret, err := GetSharedSecret(ours, pt[0], pt[1]); err == nil {
			t.Errorf("%s: chave inválida aceita (segredo %x)", name, secret)
		}
	}
}
