package backend

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
)

// Bridge é o que a janela enxerga do backend (window.go.backend.Bridge no
// Wails): um método só, que diz de que aba é cada pedido. Assim uma ação
// pedida numa aba nunca vai parar no servidor de outra, mesmo que a pessoa
// troque de aba enquanto o pedido está a caminho.
type Bridge struct {
	app *App
}

func (a *App) Bridge() *Bridge { return &Bridge{app: a} }

// Call executa um método da API na conexão da aba tab (0 = a que está à
// vista). Os argumentos vêm como a interface os mandou, em JSON.
func (b *Bridge) Call(tab int, method string, args []json.RawMessage) (any, error) {
	c := b.app.connByID(tab)
	if c == nil {
		return nil, errors.New("esta aba foi fechada")
	}
	return callAPI(c.api(), method, args)
}

var (
	apiType   = reflect.TypeOf(&API{})
	errorType = reflect.TypeOf((*error)(nil)).Elem()
)

// callAPI chama pelo nome um método exportado de API, convertendo os
// argumentos de JSON para os tipos que ele declara. Argumento que falta vale o
// zero do tipo; argumento sobrando ou de tipo errado é erro.
func callAPI(api *API, name string, args []json.RawMessage) (any, error) {
	m, ok := apiType.MethodByName(name)
	if !ok {
		return nil, fmt.Errorf("método desconhecido: %q", name)
	}
	t := m.Type // a entrada 0 é o próprio *API
	if len(args) > t.NumIn()-1 {
		return nil, fmt.Errorf("%s recebe %d argumentos, vieram %d", name, t.NumIn()-1, len(args))
	}
	in := make([]reflect.Value, t.NumIn())
	in[0] = reflect.ValueOf(api)
	for i := 1; i < t.NumIn(); i++ {
		v := reflect.New(t.In(i))
		if i-1 < len(args) && len(args[i-1]) > 0 {
			if err := json.Unmarshal(args[i-1], v.Interface()); err != nil {
				return nil, fmt.Errorf("argumento %d de %s inválido", i, name)
			}
		}
		in[i] = v.Elem()
	}
	var result any
	for _, out := range m.Func.Call(in) { // (), (T), (error) ou (T, error)
		if out.Type() == errorType {
			if !out.IsNil() {
				return nil, out.Interface().(error)
			}
			continue
		}
		result = out.Interface()
	}
	return result, nil
}
