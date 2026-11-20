package ts3

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"strconv"
)

type Identity struct {
	PrivateKey *ecdsa.PrivateKey
	PublicKey  *ecdsa.PublicKey
	OmegaStr   string
	Uid        string
	KeyOffset  uint64
}

// GenerateIdentity cria uma nova identidade TS3 válida (prime256v1)
func GenerateIdentity() (*Identity, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	omegaBase64 := buildLtcPublicKeyDerBase64(key.PublicKey.X, key.PublicKey.Y)
	uidSum := sha1.Sum([]byte(omegaBase64))
	uidBase64 := base64.StdEncoding.EncodeToString(uidSum[:])

	id := &Identity{
		PrivateKey: key,
		PublicKey:  &key.PublicKey,
		OmegaStr:   omegaBase64,
		Uid:        uidBase64,
		KeyOffset:  0,
	}

	id.ImproveSecurityLevel(8)

	return id, nil
}

type exportedIdentity struct {
	PrivateKey []byte `json:"private_key"`
	KeyOffset  uint64 `json:"key_offset"`
}

func SaveIdentity(id *Identity, filename string) error {
	pkBytes, err := id.MarshalKey()
	if err != nil {
		return err
	}

	exp := exportedIdentity{
		PrivateKey: pkBytes,
		KeyOffset:  id.KeyOffset,
	}

	fileBytes, _ := json.MarshalIndent(exp, "", "  ")
	return os.WriteFile(filename, fileBytes, 0600)
}

func LoadIdentity(filename string) (*Identity, error) {
	fileData, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	var exp exportedIdentity
	if err := json.Unmarshal(fileData, &exp); err != nil {
		return nil, err
	}
	return IdentityFromKey(exp.PrivateKey, exp.KeyOffset)
}

// MarshalKey devolve a chave privada da identidade em DER (SEC 1), o mesmo
// formato gravado por SaveIdentity.
func (id *Identity) MarshalKey() ([]byte, error) {
	return x509.MarshalECPrivateKey(id.PrivateKey)
}

// IdentityFromKey monta uma identidade a partir da chave privada em DER
// (MarshalKey) e do contador do nível de segurança.
func IdentityFromKey(der []byte, keyOffset uint64) (*Identity, error) {
	key, err := x509.ParseECPrivateKey(der)
	if err != nil {
		return nil, err
	}

	omegaBase64 := buildLtcPublicKeyDerBase64(key.PublicKey.X, key.PublicKey.Y)
	uidSum := sha1.Sum([]byte(omegaBase64))
	uidBase64 := base64.StdEncoding.EncodeToString(uidSum[:])

	return &Identity{
		PrivateKey: key,
		PublicKey:  &key.PublicKey,
		OmegaStr:   omegaBase64,
		Uid:        uidBase64,
		KeyOffset:  keyOffset,
	}, nil
}

func (id *Identity) ImproveSecurityLevel(targetLevel int) {
	pubKeyBytes := []byte(id.OmegaStr)
	offset := id.KeyOffset
	best := id.GetSecurityLevel(pubKeyBytes, offset)

	for checked := offset; ; checked++ {
		if best >= targetLevel {
			id.KeyOffset = offset
			return
		}
		curr := id.GetSecurityLevel(pubKeyBytes, checked)
		if curr > best {
			offset = checked
			best = curr
		}
	}
}

func (id *Identity) GetSecurityLevel(pubKeyBytes []byte, offset uint64) int {
	offsetStr := strconv.FormatUint(offset, 10)
	hashInput := append(append([]byte{}, pubKeyBytes...), []byte(offsetStr)...)
	h := sha1.New()
	h.Write(hashInput)
	sum := h.Sum(nil)
	return countLeadingZeroBits(sum[:])
}

func countLeadingZeroBits(data []byte) int {
	count := 0
	for i := 0; i < len(data); i++ {
		if data[i] == 0 {
			count += 8
		} else {
			for bit := 0; bit < 8; bit++ {
				if (data[i] & (1 << bit)) == 0 {
					count++
				} else {
					return count
				}
			}
		}
	}
	return count
}

func buildLtcPublicKeyDerBase64(x, y *big.Int) string {
	bitStr := []byte{0x03, 0x02, 0x07, 0x00}
	int32Buf := buildDerBigInteger(big.NewInt(32))
	intX := buildDerBigInteger(x)
	intY := buildDerBigInteger(y)

	content := new(bytes.Buffer)
	content.Write(bitStr)
	content.Write(int32Buf)
	content.Write(intX)
	content.Write(intY)

	final := new(bytes.Buffer)
	final.WriteByte(0x30)
	final.Write(buildDerLength(content.Len()))
	final.Write(content.Bytes())

	return base64.StdEncoding.EncodeToString(final.Bytes())
}

func buildDerBigInteger(value *big.Int) []byte {
	hex := fmt.Sprintf("%x", value)
	if len(hex)%2 != 0 {
		hex = "0" + hex
	}

	buf := make([]byte, len(hex)/2)
	for i := 0; i < len(hex)/2; i++ {
		fmt.Sscanf(hex[i*2:i*2+2], "%2x", &buf[i])
	}

	if (buf[0] & 0x80) != 0 {
		buf = append([]byte{0x00}, buf...)
	}

	ret := new(bytes.Buffer)
	ret.WriteByte(0x02)
	ret.Write(buildDerLength(len(buf)))
	ret.Write(buf)

	return ret.Bytes()
}

func buildDerLength(l int) []byte {
	if l < 0x80 {
		return []byte{byte(l)}
	}
	if l < 0x100 {
		return []byte{0x81, byte(l)}
	}
	return []byte{0x82, byte((l >> 8) & 0xff), byte(l & 0xff)}
}

// ParseDerSequence lê a chave pública que o servidor manda no protocolo
// antigo (uma SEQUENCE DER) e devolve as coordenadas X e Y: os dois últimos
// INTEGER. Os bytes vêm da rede; a leitura é a de derIntegers, que confere
// cada tamanho contra o que realmente chegou.
func ParseDerSequence(data []byte) (*big.Int, *big.Int, error) {
	ints, err := derIntegers(data)
	if err != nil {
		return nil, nil, err
	}
	if len(ints) < 3 {
		return nil, nil, fmt.Errorf("invalid server key format")
	}
	return ints[len(ints)-2], ints[len(ints)-1], nil
}

// SecurityLevel devolve o nível de segurança atual da identidade.
func (id *Identity) SecurityLevel() int {
	return id.GetSecurityLevel([]byte(id.OmegaStr), id.KeyOffset)
}
