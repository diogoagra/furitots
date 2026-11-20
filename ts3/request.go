package ts3

import (
	"errors"
	"strconv"
	"sync"
	"time"
)

// Pedidos com resposta. O servidor encerra a resposta de todo comando que
// leva um return_code com uma linha "error" (id=0 quando deu certo); as
// linhas de dados (listas) chegam antes dela. Um pedido por vez.

const requestTimeout = 8 * time.Second

type pendingRequest struct {
	code    string
	collect map[string]bool
	items   []map[string]string
	done    chan error
}

type requester struct {
	serial sync.Mutex // um pedido por vez
	mu     sync.Mutex
	cur    *pendingRequest
	next   int
}

// request envia um comando e espera o servidor confirmar. Devolve os itens
// das linhas cujo nome está em collect (ex.: "notifyfilelist").
func (c *Client) request(cmd string, collect ...string) ([]map[string]string, error) {
	s := c.session()
	if s == nil || !s.connected.Load() {
		return nil, errors.New("não conectado")
	}
	c.req.serial.Lock()
	defer c.req.serial.Unlock()

	p := &pendingRequest{collect: make(map[string]bool), done: make(chan error, 1)}
	for _, name := range collect {
		p.collect[name] = true
	}
	c.req.mu.Lock()
	c.req.next++
	p.code = "rq" + strconv.Itoa(c.req.next)
	c.req.cur = p
	c.req.mu.Unlock()
	defer func() {
		c.req.mu.Lock()
		c.req.cur = nil
		c.req.mu.Unlock()
	}()

	s.sendCommand(cmd + " return_code=" + p.code)
	select {
	case err := <-p.done:
		return p.items, err
	case <-s.done:
		return nil, errors.New("conexão encerrada")
	case <-time.After(requestTimeout):
		return nil, errors.New("o servidor não respondeu")
	}
}

// requestLine entrega uma linha recebida ao pedido em andamento. Devolve true
// se a linha era dele (e não deve seguir para o tratamento normal).
func (c *Client) requestLine(name string, items []map[string]string) bool {
	c.req.mu.Lock()
	defer c.req.mu.Unlock()
	p := c.req.cur
	if p == nil {
		return false
	}
	if name == "error" {
		if len(items) == 0 || items[0]["return_code"] != p.code {
			return false
		}
		var err error
		if id := items[0]["id"]; id != "" && id != "0" {
			err = &ServerError{ID: atoi(id), Message: translateError(items[0]["msg"]), Extra: items[0]["extra_msg"]}
		}
		c.req.cur = nil
		p.done <- err
		return true
	}
	if p.collect[name] {
		p.items = append(p.items, items...)
		return true
	}
	return false
}
