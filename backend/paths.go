package backend

import (
	"os"
	"path/filepath"
)

// Nome da pasta de dados do app dentro da pasta de configuração do usuário
// (macOS: ~/Library/Application Support; Windows: %AppData%), e o nome que
// ela tinha quando o app se chamava TeamSpeak Furito.
const (
	dataDirName    = "FuritoTS"
	oldDataDirName = "TeamSpeakFurito"
)

// DataDir devolve a pasta de dados do app dentro de configDir.
//
// Quem vem de uma versão anterior tem a pasta com o nome antigo: na primeira
// vez ela é renomeada, e os ajustes, o histórico, os sons e os scripts
// continuam onde estavam. Se não der para renomear, o app segue usando a
// antiga, em vez de abrir zerado.
func DataDir(configDir string) string {
	dir := filepath.Join(configDir, dataDirName)
	if _, err := os.Stat(dir); err == nil {
		return dir
	}
	old := filepath.Join(configDir, oldDataDirName)
	if _, err := os.Stat(old); err != nil {
		return dir
	}
	if err := os.Rename(old, dir); err != nil {
		return old
	}
	return dir
}
