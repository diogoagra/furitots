package backend

import "strings"

// Versão deste cliente, como aparece para os outros clientes Furito.
//
// O servidor confere a assinatura da TeamSpeak sobre o par versão+plataforma
// enviado na conexão e recusa qualquer outro texto ("client is modified"); por
// isso o cliente se apresenta ao servidor com a versão padrão assinada
// (ts3.DefaultVersion) e anuncia a versão de verdade no campo livre
// client_meta_data, que todos os clientes recebem. Clientes oficiais continuam
// mostrando a versão assinada.
const (
	AppVersion = "1.0.1"
	AppVendor  = "Diogo Agra"            // autor; aparece depois da versão
	AppSite    = "https://diogoagra.dev" // site do autor

	furitoMetaPrefix = "furito/"
)

// furitoMeta é o texto que este cliente envia em client_meta_data.
func furitoMeta() string { return furitoMetaPrefix + AppVersion }

// furitoVersion devolve a versão anunciada por um cliente Furito em
// client_meta_data ("" se o cliente não for um Furito).
func furitoVersion(meta string) string {
	rest, ok := strings.CutPrefix(meta, furitoMetaPrefix)
	if !ok {
		return ""
	}
	// só o primeiro campo; o resto fica reservado para capacidades futuras
	if i := strings.IndexAny(rest, " ;,"); i >= 0 {
		rest = rest[:i]
	}
	if rest == "" || len(rest) > 16 {
		return ""
	}
	for _, r := range rest {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '.' || r == '-') {
			return ""
		}
	}
	return rest
}
