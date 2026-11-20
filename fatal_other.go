//go:build !windows

package main

// showFatal: fora do Windows o erro já foi para a saída de erro e para o
// registro (startup.go); quem abre pelo Finder vê o registro.
func showFatal(title, text string) {}
