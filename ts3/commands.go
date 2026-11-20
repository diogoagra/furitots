package ts3

import (
	"crypto/sha1"
	"encoding/base64"
	"strings"
	"unicode/utf8"
)

// Tabela de escapes do protocolo de comandos do TeamSpeak.
var escapeReplacer = strings.NewReplacer(
	`\`, `\\`,
	`/`, `\/`,
	` `, `\s`,
	`|`, `\p`,
	"\a", `\a`,
	"\b", `\b`,
	"\f", `\f`,
	"\n", `\n`,
	"\r", `\r`,
	"\t", `\t`,
	"\v", `\v`,
)

// Escape codifica um valor para o formato de comandos do TS3.
func Escape(s string) string {
	return escapeReplacer.Replace(s)
}

// Unescape decodifica um valor do formato TS3. É feito em uma única passada:
// substituições encadeadas corrompem sequências como `\\t` (barra + "t").
func Unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch != '\\' || i+1 >= len(s) {
			b.WriteByte(ch)
			continue
		}
		i++
		switch s[i] {
		case '\\':
			b.WriteByte('\\')
		case '/':
			b.WriteByte('/')
		case 's':
			b.WriteByte(' ')
		case 'p':
			b.WriteByte('|')
		case 'a':
			b.WriteByte('\a')
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'v':
			b.WriteByte('\v')
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// Param é um par chave/valor de um comando, na ordem em que deve ser enviado.
type Param struct {
	Key   string
	Value string
}

// BuildCommand monta um comando com parâmetros em ordem determinística.
func BuildCommand(cmd string, params ...Param) string {
	var b strings.Builder
	b.WriteString(cmd)
	for _, p := range params {
		b.WriteByte(' ')
		b.WriteString(p.Key)
		b.WriteByte('=')
		b.WriteString(Escape(p.Value))
	}
	return b.String()
}

// wireSpace são os separadores do protocolo: o espaço (e as quebras de linha e
// tabulações que podem cercar um comando). Dentro de um valor todos eles vêm
// codificados (\s, \n, \t…). Outros espaços do Unicode (U+00A0, U+2028…) NÃO
// separam nada: o servidor os deixa passar dentro de apelidos e mensagens, e
// tratá-los como separador deixaria qualquer usuário acrescentar parâmetros
// falsos ao comando (outro clid, outra identidade) só escrevendo um deles no
// apelido.
const wireSpace = " \t\n\r\v\f"

func isWireSpace(r rune) bool { return strings.ContainsRune(wireSpace, r) }

// Limites de um comando recebido. Cada item herda do primeiro as chaves que
// não traz; sem limite, um comando de 16 KB (milhares de chaves no primeiro
// item e milhares de itens vazios) faria o cliente alocar gigabytes.
// Servidores de verdade mandam listas de até alguns milhares de itens, que
// herdam duas ou três chaves cada.
const (
	maxCommandItems    = 65536
	maxInheritedParams = 1 << 18
)

// maxValueLen é o maior valor aceito num parâmetro recebido. O maior que um
// servidor de verdade manda é a descrição de um canal (8192 caracteres, até
// uns 50 KB com os escapes); um comando pode chegar a megabytes, e um valor
// desse tamanho num apelido ou numa mensagem travaria a interface, que os
// recebe a cada mudança de estado.
const maxValueLen = 64 << 10

// clipValue corta um valor (ainda codificado) em maxValueLen, sem partir um
// caractere nem uma sequência de escape ao meio.
func clipValue(v string) string {
	if len(v) <= maxValueLen {
		return v
	}
	end := maxValueLen
	for end > 0 && !utf8.RuneStart(v[end]) {
		end--
	}
	slashes := 0
	for i := end - 1; i >= 0 && v[i] == '\\'; i-- {
		slashes++
	}
	if slashes%2 == 1 {
		end-- // a barra que sobraria sozinha no fim era o começo de um escape
	}
	return v[:end]
}

// ParseCommand separa uma linha recebida em nome + itens. Listas usam "|"
// entre itens; chaves que só aparecem no primeiro item (ex.: ctid, reasonid)
// valem para os demais. Chaves sem "=" são valores vazios.
func ParseCommand(line string) (name string, items []map[string]string) {
	line = strings.Trim(line, wireSpace)
	if line == "" {
		return "", nil
	}
	rest := ""
	if sp := strings.IndexByte(line, ' '); sp >= 0 {
		first := line[:sp]
		if strings.Contains(first, "=") {
			// resposta sem nome de comando (só parâmetros)
			rest = line
		} else {
			name = first
			rest = line[sp+1:]
		}
	} else if strings.Contains(line, "=") {
		rest = line
	} else {
		return line, []map[string]string{{}}
	}

	var first map[string]string
	inherited := 0
	for n, part := range strings.Split(rest, "|") {
		if n >= maxCommandItems {
			break
		}
		item := make(map[string]string)
		for _, tok := range strings.FieldsFunc(part, isWireSpace) {
			if eq := strings.IndexByte(tok, '='); eq >= 0 {
				item[tok[:eq]] = Unescape(clipValue(tok[eq+1:]))
			} else {
				item[tok] = ""
			}
		}
		if first == nil {
			first = item
		} else if inherited < maxInheritedParams {
			for k, v := range first {
				if _, ok := item[k]; !ok {
					item[k] = v
					inherited++
				}
			}
		}
		items = append(items, item)
	}
	return name, items
}

// HashPassword devolve a forma em que o cliente completo envia senhas de
// servidor/canal: base64(sha1(senha)). Senha vazia continua vazia.
func HashPassword(password string) string {
	if password == "" {
		return ""
	}
	sum := sha1.Sum([]byte(password))
	return base64.StdEncoding.EncodeToString(sum[:])
}
