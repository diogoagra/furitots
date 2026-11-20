package crypto

import (
	"crypto/ecdh"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math/big"
)

const MacLen = 8

var InitMac = []byte("TS3INIT1")
var DummyKey = []byte("c:\\windows\\syste")
var DummyNonce = []byte("m\\firewall32.cpl")

const InitVersion uint32 = 1566914096

func Sha1(data []byte) []byte {
	h := sha1.New()
	h.Write(data)
	return h.Sum(nil)
}

func Sha256(data []byte) []byte {
	h := sha256.New()
	h.Write(data)
	return h.Sum(nil)
}

func XorInto(a, b []byte, l int) {
	if l > len(a) {
		l = len(a)
	}
	if l > len(b) {
		l = len(b)
	}
	for i := 0; i < l; i++ {
		a[i] ^= b[i]
	}
}

func DeriveKeyNonce(fromServer bool, packetId uint16, generationId uint32, packetType byte, ivStruct []byte) (key []byte, nonce []byte) {
	tmpLen := 70
	if len(ivStruct) == 20 {
		tmpLen = 26
	}
	tmp := make([]byte, tmpLen)

	if fromServer {
		tmp[0] = 0x30
	} else {
		tmp[0] = 0x31
	}
	tmp[1] = packetType

	binary.BigEndian.PutUint32(tmp[2:6], generationId)
	copy(tmp[6:], ivStruct)

	keynonce := Sha256(tmp)
	key = make([]byte, 16)
	nonce = make([]byte, 16)
	copy(key, keynonce[0:16])
	copy(nonce, keynonce[16:32])

	key[0] ^= byte((packetId >> 8) & 0xff)
	key[1] ^= byte(packetId & 0xff)

	return key, nonce
}

// GetSharedSecret calcula o segredo do protocolo antigo: SHA-1 da coordenada X
// do ponto combinado (ECDH na curva P-256) entre a nossa chave e a chave
// pública que o servidor mandou.
//
// A chave do servidor vem da rede. Um ponto fora da curva (ou o ponto no
// infinito) é recusado aqui: aceitá-lo derrubaria o programa e, pior, é a
// porta para o ataque que descobre a chave privada aos poucos, com pontos de
// curvas fracas.
func GetSharedSecret(privateKey *ecdh.PrivateKey, serverPubX *big.Int, serverPubY *big.Int) ([]byte, error) {
	if privateKey == nil || serverPubX == nil || serverPubY == nil ||
		serverPubX.Sign() < 0 || serverPubY.Sign() < 0 || serverPubX.BitLen() > 256 || serverPubY.BitLen() > 256 {
		return nil, errors.New("chave pública do servidor inválida")
	}
	point := make([]byte, 65) // 0x04, X e Y com 32 bytes cada
	point[0] = 4
	serverPubX.FillBytes(point[1:33])
	serverPubY.FillBytes(point[33:65])
	pub, err := ecdh.P256().NewPublicKey(point)
	if err != nil {
		return nil, errors.New("chave pública do servidor inválida: o ponto não está na curva")
	}
	shared, err := privateKey.ECDH(pub)
	if err != nil {
		return nil, err
	}
	return Sha1(shared), nil
}
