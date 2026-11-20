package ts3

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"strings"
)

// Formato de identidade do cliente TeamSpeak 3 (Ferramentas → Identidades →
// Exportar), um arquivo .ini:
//
//	[Identity]
//	id=Nome da identidade
//	identity="<contador>V<chave ofuscada em base64>"
//	nickname=Apelido
//
// O contador é o número que dá o nível de segurança (KeyOffset). A chave é a
// estrutura do libtomcrypt (SEQUENCE { BIT STRING, INTEGER 32, X, Y, K }) em
// base64, com os 100 primeiros caracteres embaralhados por uma chave fixa do
// cliente e os 20 primeiros também pelo SHA-1 do restante.

// ts3ObfuscationKey é a chave fixa com que o cliente TeamSpeak 3 embaralha as
// identidades exportadas.
const ts3ObfuscationKey = "b9dfaa7bee6ac57ac7b65f1094a1c155e747327bc2fe5d51c512023fe54a280201004e90ad1daaae1075d53b7d571c30e063b5a62a4a017bb394833aa0983e6e"

// LoadIdentityFile lê uma identidade de um arquivo: o formato deste app
// (JSON), o .ini exportado pelo TeamSpeak 3 ou só o texto "<n>V<base64>".
func LoadIdentityFile(filename string) (*Identity, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	text := strings.TrimSpace(strings.TrimPrefix(string(data), "\ufeff"))
	if strings.HasPrefix(text, "{") {
		return LoadIdentity(filename)
	}
	return ParseTS3Identity(text)
}

// TS3IdentityInfo são os campos de texto do .ini exportado pelo TeamSpeak 3.
type TS3IdentityInfo struct {
	Name     string // id=
	Nickname string // nickname=
}

// ParseTS3IdentityInfo lê o nome e o apelido de um .ini do TeamSpeak 3 (vazios
// se o texto não for um .ini).
func ParseTS3IdentityInfo(text string) TS3IdentityInfo {
	var info TS3IdentityInfo
	for _, line := range strings.Split(text, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"`)
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "id":
			info.Name = v
		case "nickname":
			info.Nickname = v
		}
	}
	return info
}

// ParseTS3Identity lê uma identidade exportada pelo TeamSpeak 3: o conteúdo
// do .ini ou só o valor do campo identity.
func ParseTS3Identity(text string) (*Identity, error) {
	value := strings.TrimSpace(text)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if k, v, ok := strings.Cut(line, "="); ok && strings.EqualFold(strings.TrimSpace(k), "identity") {
			value = strings.TrimSpace(v)
			break
		}
	}
	value = strings.Trim(value, `"'`)

	counter, obfuscated, ok := strings.Cut(value, "V")
	if !ok {
		return nil, errors.New("não é uma identidade do TeamSpeak 3 (falta o campo identity=\"…V…\")")
	}
	offset, err := strconv.ParseUint(counter, 10, 64)
	if err != nil {
		return nil, errors.New("identidade do TeamSpeak 3 inválida (contador do nível de segurança)")
	}
	blob, err := base64.StdEncoding.DecodeString(obfuscated)
	if err != nil || len(blob) < 20 {
		return nil, errors.New("identidade do TeamSpeak 3 inválida (base64)")
	}

	// Desfaz o embaralhamento: SHA-1 do trecho depois dos 20 primeiros bytes
	// (até o primeiro byte zero) sobre os 20 primeiros; depois a chave fixa
	// sobre os 100 primeiros.
	rest := blob[20:]
	if i := bytes.IndexByte(rest, 0); i >= 0 {
		rest = rest[:i]
	}
	sum := sha1.Sum(rest)
	for i := 0; i < 20; i++ {
		blob[i] ^= sum[i]
	}
	n := len(blob)
	if n > 100 {
		n = 100
	}
	for i := 0; i < n; i++ {
		blob[i] ^= ts3ObfuscationKey[i]
	}
	if i := bytes.IndexByte(blob, 0); i >= 0 {
		blob = blob[:i]
	}
	der, err := base64.StdEncoding.DecodeString(string(blob))
	if err != nil {
		return nil, errors.New("identidade do TeamSpeak 3 inválida (não foi possível desembaralhar a chave)")
	}
	key, err := parseLtcPrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("identidade do TeamSpeak 3 inválida: %w", err)
	}

	omega := buildLtcPublicKeyDerBase64(key.PublicKey.X, key.PublicKey.Y)
	uid := sha1.Sum([]byte(omega))
	return &Identity{
		PrivateKey: key,
		PublicKey:  &key.PublicKey,
		OmegaStr:   omega,
		Uid:        base64.StdEncoding.EncodeToString(uid[:]),
		KeyOffset:  offset,
	}, nil
}

// parseLtcPrivateKey lê a chave no formato do libtomcrypt:
// SEQUENCE { BIT STRING marcas, INTEGER tamanho, [INTEGER X, INTEGER Y,] INTEGER K }.
// Versões mais novas do cliente gravam só K, sem o ponto público.
func parseLtcPrivateKey(der []byte) (*ecdsa.PrivateKey, error) {
	ints, err := derIntegers(der)
	if err != nil {
		return nil, err
	}
	var k, x, y *big.Int
	switch len(ints) {
	case 4: // tamanho, X, Y, K
		x, y, k = ints[1], ints[2], ints[3]
	case 2: // tamanho, K
		k = ints[1]
	case 3:
		return nil, errors.New("o arquivo só tem a chave pública (sem a chave privada não dá para usar a identidade)")
	default:
		return nil, fmt.Errorf("estrutura de chave inesperada (%d números)", len(ints))
	}
	if ints[0].Cmp(big.NewInt(32)) != 0 {
		return nil, errors.New("curva não suportada (esperado P-256)")
	}
	curve := elliptic.P256()
	if k.Sign() <= 0 || k.Cmp(curve.Params().N) >= 0 {
		return nil, errors.New("chave privada fora do intervalo da curva")
	}
	px, py := curve.ScalarBaseMult(k.Bytes())
	if x != nil && (x.Cmp(px) != 0 || y.Cmp(py) != 0) {
		return nil, errors.New("a chave pública do arquivo não corresponde à chave privada")
	}
	return &ecdsa.PrivateKey{PublicKey: ecdsa.PublicKey{Curve: curve, X: px, Y: py}, D: k}, nil
}

// derIntegers devolve os INTEGER de uma SEQUENCE DER, na ordem.
func derIntegers(data []byte) ([]*big.Int, error) {
	bad := errors.New("estrutura de chave corrompida")
	length := func(pos int) (n, size int, ok bool) {
		if pos >= len(data) {
			return 0, 0, false
		}
		if data[pos] < 0x80 {
			return int(data[pos]), 1, true
		}
		count := int(data[pos] & 0x7f)
		if count == 0 || count > 2 || pos+count >= len(data) {
			return 0, 0, false
		}
		for i := 1; i <= count; i++ {
			n = n<<8 | int(data[pos+i])
		}
		return n, 1 + count, true
	}
	if len(data) < 2 || data[0] != 0x30 {
		return nil, bad
	}
	n, size, ok := length(1)
	if !ok || 1+size+n > len(data) {
		return nil, bad
	}
	pos, end := 1+size, 1+size+n
	var out []*big.Int
	for pos < end {
		tag := data[pos]
		n, size, ok := length(pos + 1)
		if !ok || pos+1+size+n > end {
			return nil, bad
		}
		body := data[pos+1+size : pos+1+size+n]
		if tag == 0x02 {
			out = append(out, new(big.Int).SetBytes(body))
		}
		pos += 1 + size + n
	}
	return out, nil
}
