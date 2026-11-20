package ts3

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"errors"

	"filippo.io/edwards25519"
)

var LicenseRootKey = []byte{
	0xcd, 0x0d, 0xe2, 0xae, 0xd4, 0x63, 0x45, 0x50, 0x9a, 0x7e, 0x3c, 0xfd,
	0x8f, 0x68, 0xb3, 0xdc, 0x75, 0x55, 0xb2, 0x9d, 0xcc, 0xec, 0x73, 0xcd,
	0x18, 0x75, 0x0f, 0x99, 0x38, 0x12, 0x40, 0x8a,
}

type LicenseBlock struct {
	Key  []byte
	Hash []byte
	Type uint8
}

func parseLicense(data []byte) ([]LicenseBlock, error) {
	if len(data) < 1 {
		return nil, errors.New("license too short")
	}
	if data[0] != 1 {
		return nil, errors.New("unsupported license version")
	}

	var blocks []LicenseBlock
	pos := 1

	for pos < len(data) {
		if len(data)-pos < 42 {
			return nil, errors.New("license block too short")
		}
		if data[pos] != 0 {
			return nil, errors.New("wrong key kind")
		}

		key := data[pos+1 : pos+33]
		blockType := data[pos+33]

		extraLen := 0
		switch blockType {
		case 0:
			extraLen = findNullTerminator(data, pos+46) + 5
		case 2:
			extraLen = findNullTerminator(data, pos+47) + 6
		case 8:
			if len(data) < pos+44 {
				return nil, errors.New("invalid TS5 license block")
			}
			p := pos + 44
			propCount := int(data[pos+43])
			for i := 0; i < propCount; i++ {
				if len(data) <= p {
					return nil, errors.New("out of bounds TS5 properties")
				}
				propLen := int(data[p])
				p += 1 + propLen
			}
			extraLen = p - (pos + 42)
		case 32:
			extraLen = 0
		default:
			return nil, errors.New("invalid license block type")
		}

		blockLen := 42 + extraLen
		if pos+blockLen > len(data) {
			return nil, errors.New("block length exceeds data")
		}
		hashInput := data[pos+1 : pos+blockLen]
		h := sha512.Sum512(hashInput)

		blocks = append(blocks, LicenseBlock{
			Key:  append([]byte(nil), key...),
			Hash: append([]byte(nil), h[:32]...),
			Type: blockType,
		})
		pos += blockLen
	}

	return blocks, nil
}

func findNullTerminator(data []byte, start int) int {
	for i := start; i < len(data); i++ {
		if data[i] == 0 {
			return i - start
		}
	}
	return -1
}

func deriveLicenseKey(blocks []LicenseBlock) ([]byte, error) {
	parentKey := append([]byte(nil), LicenseRootKey...)

	for _, block := range blocks {
		var err error
		parentKey, err = deriveBlockKey(block.Key, block.Hash, parentKey)
		if err != nil {
			return nil, err
		}
	}
	return parentKey, nil
}

func deriveBlockKey(blockKey, hash, parentKey []byte) ([]byte, error) {
	scalar := make([]byte, 32)
	copy(scalar, hash)
	scalar[0] &= 0xf8
	scalar[31] &= 0x3f
	scalar[31] |= 0x40

	s, err := new(edwards25519.Scalar).SetBytesWithClamping(scalar)
	if err != nil {
		return nil, err
	}

	p, err := new(edwards25519.Point).SetBytes(blockKey)
	if err != nil {
		return nil, err
	}

	mulResult := new(edwards25519.Point).ScalarMult(s, p)

	parentPoint, err := new(edwards25519.Point).SetBytes(parentKey)
	if err != nil {
		return nil, err
	}

	result := new(edwards25519.Point).Add(mulResult, parentPoint)
	return result.Bytes(), nil
}

func generateTemporaryKey() (pub []byte, priv []byte, err error) {
	priv = make([]byte, 32)
	_, err = rand.Read(priv)
	if err != nil {
		return nil, nil, err
	}
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64

	s, err := new(edwards25519.Scalar).SetBytesWithClamping(priv)
	if err != nil {
		return nil, nil, err
	}

	pubPoint := new(edwards25519.Point).ScalarBaseMult(s)
	pub = pubPoint.Bytes()

	return pub, priv, nil
}

func getSharedSecret2(serverDerivedKey, tempPrivateKey []byte) ([]byte, error) {
	privCopy := append([]byte(nil), tempPrivateKey...)
	privCopy[31] &= 0x7f

	s, err := new(edwards25519.Scalar).SetBytesWithClamping(privCopy)
	if err != nil {
		return nil, err
	}

	p, err := new(edwards25519.Point).SetBytes(serverDerivedKey)
	if err != nil {
		return nil, err
	}

	qPoint := new(edwards25519.Point).ScalarMult(s, p)
	q := qPoint.Bytes()

	h := sha512.Sum512(q)
	return h[:], nil
}

func generateClientEk(tempPub []byte, tempPriv []byte, beta []byte, identity *Identity) (string, error) {
	ekBase64 := base64.StdEncoding.EncodeToString(tempPub)

	toSign := append([]byte(nil), tempPub...)
	toSign = append(toSign, beta...)

	h := sha256.Sum256(toSign)
	sig, err := identity.PrivateKey.Sign(rand.Reader, h[:], nil)
	if err != nil {
		return "", err
	}

	proofBase64 := base64.StdEncoding.EncodeToString(sig)

	return "clientek ek=" + ekBase64 + " proof=" + proofBase64, nil
}
