//go:build !devhooks

package main

import (
	"context"

	"furito-ts/backend"
)

// O app que vai para os usuários não tem ganchos de desenvolvimento: nenhuma
// variável de ambiente faz a janela executar código. Ver devhooks.go.

func devBindings(*backend.App) []interface{} { return nil }

func devDomReady(context.Context) {}
