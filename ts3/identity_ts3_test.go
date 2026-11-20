package ts3

import (
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// ts3Export monta o texto "<contador>V<base64>" como o cliente TeamSpeak 3
// exporta: o caminho inverso de ParseTS3Identity.
func ts3Export(id *Identity, withPublic bool) string {
	content := new(bytes.Buffer)
	if withPublic {
		content.Write([]byte{0x03, 0x02, 0x07, 0x80})
	} else {
		content.Write([]byte{0x03, 0x02, 0x06, 0xC0})
	}
	content.Write(buildDerBigInteger(bigInt(32)))
	if withPublic {
		content.Write(buildDerBigInteger(id.PublicKey.X))
		content.Write(buildDerBigInteger(id.PublicKey.Y))
	}
	content.Write(buildDerBigInteger(id.PrivateKey.D))
	der := append([]byte{0x30}, buildDerLength(content.Len())...)
	der = append(der, content.Bytes()...)

	blob := []byte(base64.StdEncoding.EncodeToString(der))
	for i := 0; i < len(blob) && i < 100; i++ {
		blob[i] ^= ts3ObfuscationKey[i]
	}
	rest := blob[20:]
	if i := bytes.IndexByte(rest, 0); i >= 0 {
		rest = rest[:i]
	}
	sum := sha1.Sum(rest)
	for i := 0; i < 20; i++ {
		blob[i] ^= sum[i]
	}
	return strconv.FormatUint(id.KeyOffset, 10) + "V" + base64.StdEncoding.EncodeToString(blob)
}

func TestParseTS3Identity(t *testing.T) {
	for round := 0; round < 20; round++ { // chaves diferentes: zeros no meio do trecho embaralhado etc.
		want, err := GenerateIdentity()
		if err != nil {
			t.Fatal(err)
		}
		for _, withPublic := range []bool{true, false} {
			value := ts3Export(want, withPublic)
			inputs := map[string]string{
				"só o valor":        value,
				"valor entre aspas": `"` + value + `"`,
				"arquivo .ini":      "[Identity]\nid=Teste\nidentity=\"" + value + "\"\nnickname=Fulano\nphonetic_nickname=\n",
				"ini do Windows":    "\ufeff[Identity]\r\nid=Teste\r\nidentity=\"" + value + "\"\r\nnickname=Fulano\r\n",
			}
			for name, in := range inputs {
				got, err := ParseTS3Identity(in)
				if err != nil {
					t.Fatalf("%s (ponto público=%v): %v", name, withPublic, err)
				}
				if got.Uid != want.Uid || got.KeyOffset != want.KeyOffset || got.PrivateKey.D.Cmp(want.PrivateKey.D) != 0 || got.OmegaStr != want.OmegaStr {
					t.Fatalf("%s (ponto público=%v): identidade diferente da exportada", name, withPublic)
				}
				if got.SecurityLevel() != want.SecurityLevel() {
					t.Fatalf("%s: nível de segurança %d, esperado %d", name, got.SecurityLevel(), want.SecurityLevel())
				}
			}
		}
	}
}

func TestParseTS3IdentityRejectsGarbage(t *testing.T) {
	id, _ := GenerateIdentity()
	good := ts3Export(id, true)
	tampered := []byte(good)
	tampered[len(tampered)-10] ^= 1 // estraga o fim (parte não embaralhada da chave)
	for name, in := range map[string]string{
		"vazio":             "",
		"texto qualquer":    "isto não é uma identidade",
		"ini sem identity":  "[Identity]\nid=Teste\nnickname=Fulano\n",
		"contador inválido": "abcV" + strings.SplitN(good, "V", 2)[1],
		"base64 inválido":   "12V###",
		"curto demais":      "12V" + base64.StdEncoding.EncodeToString([]byte("curto")),
		"adulterado":        string(tampered),
	} {
		if _, err := ParseTS3Identity(in); err == nil {
			t.Errorf("%s: deveria ser recusado", name)
		}
	}
}

// LoadIdentityFile aceita os dois formatos: o deste app e o do TeamSpeak 3.
func TestLoadIdentityFileFormats(t *testing.T) {
	want, _ := GenerateIdentity()
	dir := t.TempDir()

	own := filepath.Join(dir, "furito.json")
	if err := SaveIdentity(want, own); err != nil {
		t.Fatal(err)
	}
	ini := filepath.Join(dir, "ts3.ini")
	os.WriteFile(ini, []byte("[Identity]\nid=X\nidentity=\""+ts3Export(want, true)+"\"\nnickname=Y\n"), 0600)

	for _, file := range []string{own, ini} {
		got, err := LoadIdentityFile(file)
		if err != nil {
			t.Fatalf("%s: %v", filepath.Base(file), err)
		}
		if got.Uid != want.Uid || got.KeyOffset != want.KeyOffset {
			t.Errorf("%s: identidade diferente", filepath.Base(file))
		}
	}
	bad := filepath.Join(dir, "nada.txt")
	os.WriteFile(bad, []byte("olá"), 0600)
	if _, err := LoadIdentityFile(bad); err == nil {
		t.Error("arquivo qualquer deveria ser recusado")
	}
}

// Identidade real exportada pelo cliente TeamSpeak 3, lida de um arquivo
// indicado por variável de ambiente. Só mostra dados públicos (UID e nível),
// nunca a chave.
func TestRealTS3IdentityFile(t *testing.T) {
	file := os.Getenv("FURITO_TS3_IDENTITY_FILE")
	if file == "" {
		t.Skip("defina FURITO_TS3_IDENTITY_FILE=/caminho/identidade.ini")
	}
	id, err := LoadIdentityFile(file)
	if err != nil {
		t.Fatalf("não foi possível ler a identidade: %v", err)
	}
	t.Logf("identidade lida: UID %s, nível de segurança %d (contador %d)", id.Uid, id.SecurityLevel(), id.KeyOffset)
	if id.SecurityLevel() < 8 {
		t.Errorf("nível de segurança %d: servidores exigem 8 ou mais", id.SecurityLevel())
	}
}

func bigInt(n int64) *big.Int { return big.NewInt(n) }
